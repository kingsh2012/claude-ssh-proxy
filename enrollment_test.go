package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agentwire"
)

func enrollmentFixture(t *testing.T) (*Proxy, *API, string, int64) {
	t.Helper()
	s := openTestStore(t)
	p, err := NewProxy(s, filepath.Join(t.TempDir(), "host-key"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateAdminUser("admin", "test-password"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetAdminPassword("admin", "test-password"); err != nil {
		t.Fatal(err)
	}
	a := NewAPI(s, p)
	session, err := a.issueToken("admin")
	if err != nil {
		t.Fatal(err)
	}
	cid, err := s.CreateClientCredential(ClientCredential{Label: "LLM", AuthType: "password", Password: "test-password"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, a, session, cid
}

func requestEnrollment(t *testing.T, a *API, session string, cid int64) (int64, string, string) {
	t.Helper()
	body := fmt.Sprintf(`{"label":"test enrollment","server_url":"wss://example.com/agent","client_credential_ids":[%d]}`, cid)
	r := httptest.NewRequest("POST", "/api/agent-enrollments", strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("enrollment status=%d", w.Code)
	}
	var result struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("invalid enrollment response")
	}
	u, secret, err := agentwire.ParseEnrollmentToken(result.Token)
	if err != nil || u != "wss://example.com/agent" {
		t.Fatal("token destination invalid")
	}
	return result.ID, result.Token, secret
}

func TestEnrollmentRegistersHostnameReconnectsAndRevokes(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	eid, full, secret := requestEnrollment(t, a, session, cid)
	h := httptest.NewServer(http.HandlerFunc(p.agents.ServeHTTP))
	defer h.Close()
	u := "ws" + strings.TrimPrefix(h.URL, "http") + "/agent?hostname=WIN-ES"
	connect := func() *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial(u, http.Header{"Authorization": []string{"Bearer " + secret}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	c := connect()
	var server *ServerRecord
	deadline := time.Now().Add(3 * time.Second)
	for {
		server, _ = p.store.GetServer("WIN-ES")
		if server != nil && p.agents.Online(server.ID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("automatic registration failed")
		}
		time.Sleep(time.Millisecond)
	}
	if server.TargetHost != "WIN-ES" || server.ConnectionType != "agent" || len(server.ClientCredentialLabels) != 1 {
		t.Fatal("hostname or LLM permissions not inherited")
	}
	c.Close()
	for p.agents.Online(server.ID) {
		if time.Now().After(deadline) {
			t.Fatal("disconnect did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	c = connect()
	for !p.agents.Online(server.ID) {
		if time.Now().After(deadline) {
			t.Fatal("reconnect failed")
		}
		time.Sleep(time.Millisecond)
	}
	var count int
	p.store.db.QueryRow(`SELECT count(*) FROM servers`).Scan(&count)
	if count != 1 {
		t.Fatal("reconnect duplicated device")
	}
	request := httptest.NewRequest("GET", "/api/agent-enrollments", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, request)
	if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), full) || !strings.Contains(w.Body.String(), "WIN-ES") {
		t.Fatal("invalid enrollment list or secret leak")
	}
	request = httptest.NewRequest("POST", fmt.Sprintf("/api/agent-enrollments/%d/revoke", eid), nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	w = httptest.NewRecorder()
	a.Router().ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatal("revocation failed")
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("revoked connection still active")
	}
	c2, resp, err := websocket.DefaultDialer.Dial(u, http.Header{"Authorization": []string{"Bearer " + secret}})
	if c2 != nil {
		c2.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatal("revoked token accepted")
	}
	resp.Body.Close()
}

func TestEnrollmentCollisionAndDeletionCannotRecreate(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	if err := p.store.UpsertServer(ServerRecord{ProxyUser: "WIN-ES", TargetHost: "existing", TargetPort: 22}); err != nil {
		t.Fatal(err)
	}
	_, _, secret := requestEnrollment(t, a, session, cid)
	hash := agentTokenHash(secret)
	id, err := p.store.authenticateAgent(hash, "WIN-ES", true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.store.GetServer("WIN-ES-2")
	if err != nil || r.ID != id {
		t.Fatal("hostname collision overwrote an existing host")
	}
	if _, err = p.store.authenticateAgent(hash, "ANOTHER-PC", true); err == nil {
		t.Fatal("token reused on a different hostname")
	}
	if err = p.store.DeleteServer("WIN-ES-2"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.store.authenticateAgent(hash, "WIN-ES", true); err == nil {
		t.Fatal("deleted host was recreated by used token")
	}
}

func TestConcurrentEnrollmentCreatesAtMostOneDevice(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	_, _, secret := requestEnrollment(t, a, session, cid)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.store.authenticateAgent(agentTokenHash(secret), "WIN-RACE", true) }()
	}
	wg.Wait()
	var count int
	if err := p.store.db.QueryRow(`SELECT count(*) FROM servers`).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent enrollment created duplicate records")
	}
}

func TestEnrollmentRequiresAdminAndValidPermissions(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	for _, tc := range []struct {
		body, session string
		status        int
	}{
		{fmt.Sprintf(`{"server_url":"wss://example.com/agent","client_credential_ids":[%d]}`, cid), "", 401},
		{`{"server_url":"ws://example.com/agent","client_credential_ids":[1]}`, session, 400},
		{`{"server_url":"wss://example.com/agent","client_credential_ids":[]}`, session, 400},
		{`{"server_url":"wss://example.com/agent","client_credential_ids":[99999]}`, session, 400},
	} {
		r := httptest.NewRequest("POST", "/api/agent-enrollments", strings.NewReader(tc.body))
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tc.session})
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status=%d want=%d", w.Code, tc.status)
		}
	}
	var count int
	p.store.db.QueryRow(`SELECT count(*) FROM agent_enrollments`).Scan(&count)
	if count != 0 {
		t.Fatal("invalid requests left enrollments behind")
	}
}

func agentTokenHash(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}
