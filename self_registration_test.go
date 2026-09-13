package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingsh2012/ops-ssh-proxy/internal/agentwire"
)

func registrationRequest(a *API, session, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/settings/agent-registration", strings.NewReader(body))
	if session != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	}
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	return w
}

func createSharedToken(t *testing.T, a *API, session string, cid int64) (string, string) {
	t.Helper()
	w := registrationRequest(a, session, "PUT", fmt.Sprintf(`{"server_url":"wss://example.com/agent","client_credential_ids":[%d]}`, cid))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create status %d", w.Code)
	}
	var value SelfRegistration
	if json.Unmarshal(w.Body.Bytes(), &value) != nil {
		t.Fatal("invalid response")
	}
	_, secret, err := agentwire.ParseEnrollmentToken(value.Token)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(secret))
	return secret, hex.EncodeToString(sum[:])
}

func TestSharedRegistrationMultipleHostsReconnectRotationAndDeletion(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	_, hash := createSharedToken(t, a, session, cid)
	ids := map[string]int64{}
	for _, name := range []string{"es-windows-01", "WIN-SECOND"} {
		id, peerHash, err := p.store.resolveAgent(hash, name, true)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
		s, err := p.store.GetServer(name)
		if err != nil || s.ProxyUser != name || len(s.ClientCredentialLabels) != 1 || s.AgentTokenHash != peerHash || peerHash == hash {
			t.Fatal("registration name, credentials or hash mismatch")
		}
		again, _, err := p.store.resolveAgent(hash, strings.ToUpper(name), true)
		if err != nil || again != id {
			t.Fatal("reconnect duplicated host")
		}
	}
	if ids["es-windows-01"] == ids["WIN-SECOND"] {
		t.Fatal("shared key merged different hosts")
	}
	var encrypted string
	p.store.db.QueryRow(`SELECT token_encrypted FROM agent_registration_settings`).Scan(&encrypted)
	if !strings.HasPrefix(encrypted, encryptedSecretPrefix) {
		t.Fatal("token not encrypted")
	}
	w := registrationRequest(a, session, "GET", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("settings read failed")
	}
	// Bad credential references roll back the entire replacement.
	w = registrationRequest(a, session, "PUT", `{"server_url":"wss://example.com/agent","client_credential_ids":[999999]}`)
	if w.Code != 400 {
		t.Fatal("unknown credential accepted")
	}
	if _, _, err := p.store.resolveAgent(hash, "es-windows-01", true); err != nil {
		t.Fatal("failed update destroyed valid key")
	}
	_, next := createSharedToken(t, a, session, cid)
	if _, _, err := p.store.resolveAgent(hash, "es-windows-01", true); err == nil {
		t.Fatal("rotated key still accepted")
	}
	id, _, err := p.store.resolveAgent(next, "es-windows-01", true)
	if err != nil || id != ids["es-windows-01"] {
		t.Fatal("new key did not reuse route")
	}
	if err := p.store.DeleteServer("WIN-SECOND"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.store.resolveAgent(next, "WIN-SECOND", true); err == nil {
		t.Fatal("deleted route resurrected")
	}
	if w = registrationRequest(a, session, "DELETE", ""); w.Code != 200 {
		t.Fatal("disable failed")
	}
	if _, _, err := p.store.resolveAgent(next, "es-windows-01", true); err == nil {
		t.Fatal("disabled key accepted")
	}
}

func TestSharedRegistrationAuthorizationCollisionsAndConcurrentClaims(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if registrationRequest(a, "", method, "{}").Code != 401 {
			t.Fatal("unauthenticated settings access")
		}
	}
	_, hash := createSharedToken(t, a, session, cid)
	if err := p.store.UpsertServer(ServerRecord{ProxyUser: "existing-ssh", TargetHost: "127.0.0.1", TargetPort: 22}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"existing-ssh", "EXISTING-SSH", "", "bad/name", "-bad", strings.Repeat("x", 254)} {
		if _, _, err := p.store.resolveAgent(hash, name, true); err == nil {
			t.Fatalf("invalid/colliding name accepted: %q", name)
		}
	}
	var wg sync.WaitGroup
	ids := make(chan int64, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { id, _, err := p.store.resolveAgent(hash, "concurrent-host", true); ids <- id; errs <- err })
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first int64
	for id := range ids {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatal("concurrent registration duplicated host")
		}
	}
	p.store.db.Exec(`UPDATE servers SET enabled=0 WHERE id=?`, first)
	if _, _, err := p.store.resolveAgent(hash, "concurrent-host", true); err == nil {
		t.Fatal("disabled host reconnected")
	}
}

func TestSharedRegistrationRevocationClosesWebSocket(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	secret, _ := createSharedToken(t, a, session, cid)
	h := httptest.NewServer(http.HandlerFunc(p.agents.ServeHTTP))
	defer h.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.URL, "http")+"/agent?hostname=es-custom", http.Header{"Authorization": []string{"Bearer " + secret}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s, _ := p.store.GetServer("es-custom")
		if s != nil && p.agents.Online(s.ID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent not online")
		}
		time.Sleep(time.Millisecond)
	}
	createSharedToken(t, a, session, cid)
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = c.ReadMessage(); err == nil {
		t.Fatal("rotation did not close socket")
	}
}
