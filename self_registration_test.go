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
		if err != nil || s.ProxyUser != name || len(s.ClientCredentialLabels) != 0 || s.AgentTokenHash != peerHash || peerHash == hash {
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

func TestRegistrationDraftRequiresSave(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	_, oldHash := createSharedToken(t, a, session, cid)
	generate := func(auth string, address string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"server_url": address})
		r := httptest.NewRequest("POST", "/api/settings/agent-registration/generate", strings.NewReader(string(body)))
		if auth != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: auth})
		}
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		return w
	}
	if generate("", "wss://example.com/agent").Code != 401 {
		t.Fatal("unauthenticated generation accepted")
	}
	if generate(session, "http://example.com/agent").Code != 400 {
		t.Fatal("invalid address accepted")
	}
	w := generate(session, "wss://example.com/agent")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("generation failed")
	}
	var draft struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(w.Body.Bytes(), &draft) != nil {
		t.Fatal("invalid draft")
	}
	_, secret, err := agentwire.ParseEnrollmentToken(draft.Token)
	if err != nil {
		t.Fatal("invalid token")
	}
	sum := sha256.Sum256([]byte(secret))
	hash := hex.EncodeToString(sum[:])
	if _, _, err := p.store.resolveAgent(hash, "draft-host", true); err == nil {
		t.Fatal("unsaved draft accepted")
	}
	if _, _, err := p.store.resolveAgent(oldHash, "old-host", true); err != nil {
		t.Fatal("generation revoked active key")
	}
	save := func(token, address string, credential int64) int {
		body, _ := json.Marshal(map[string]any{"token": token, "server_url": address, "client_credential_ids": []int64{credential}})
		return registrationRequest(a, session, "PUT", string(body)).Code
	}
	if save(draft.Token, "wss://other.example.com/agent", cid) != 400 {
		t.Fatal("mismatched address accepted")
	}
	if save("", "wss://example.com/agent", cid) != 400 {
		t.Fatal("empty draft accepted")
	}
	if _, _, err := p.store.resolveAgent(oldHash, "old-host", true); err != nil {
		t.Fatal("failed save revoked active key")
	}
	if save(draft.Token, "wss://example.com/agent", cid) != 200 {
		t.Fatal("save failed")
	}
	current, err := p.store.selfRegistration()
	if err != nil || current.Token != draft.Token {
		t.Fatal("save changed draft key")
	}
	if _, _, err := p.store.resolveAgent(hash, "draft-host", true); err != nil {
		t.Fatal("saved draft rejected")
	}
	if _, _, err := p.store.resolveAgent(oldHash, "old-host", true); err == nil {
		t.Fatal("old key remains active")
	}
	if save(draft.Token, "wss://example.com/agent", cid) != 200 {
		t.Fatal("repeat save failed")
	}
	current, _ = p.store.selfRegistration()
	if current.Token != draft.Token {
		t.Fatal("repeat save rotated key")
	}
}

func TestRegistrationAddressAndManualAuthorization(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	saveAddress := func(address string) SelfRegistration {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"server_url": address})
		r := httptest.NewRequest("PUT", "/api/settings/agent-registration/address", strings.NewReader(string(body)))
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("address save: %d", w.Code)
		}
		var value SelfRegistration
		if json.Unmarshal(w.Body.Bytes(), &value) != nil {
			t.Fatal("invalid address response")
		}
		return value
	}
	value := saveAddress("wss://example.com/agent")
	if value.Enabled || value.Token != "" {
		t.Fatal("saving address enabled registration")
	}
	w := registrationRequest(a, session, "PUT", `{"server_url":"wss://example.com/agent"}`)
	if w.Code != 200 {
		t.Fatal("registration still requires default credentials")
	}
	current, _ := p.store.selfRegistration()
	_, secret, _ := agentwire.ParseEnrollmentToken(current.Token)
	sum := sha256.Sum256([]byte(secret))
	hash := hex.EncodeToString(sum[:])
	// Historical defaults must never grant access to newly registered hosts.
	if _, err := p.store.db.Exec(`INSERT INTO agent_registration_credentials(client_credential_id) VALUES(?)`, cid); err != nil {
		t.Fatal(err)
	}
	id, _, err := p.store.resolveAgent(hash, "manual-host", true)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := p.store.ListClientCredentialsForServerID(id)
	if err != nil || len(creds) != 0 {
		t.Fatal("registration granted access automatically")
	}
	// Use the same store operation as the manual credential edit API.
	if err := p.store.UpdateClientCredential(cid, ClientCredential{Label: "LLM", AuthType: "password", Password: "test-password"}, []string{"manual-host"}); err != nil {
		t.Fatal(err)
	}
	value = saveAddress("wss://new.example.com/agent")
	url, nextSecret, err := agentwire.ParseEnrollmentToken(value.Token)
	if err != nil || url != "wss://new.example.com/agent" || nextSecret != secret || !value.Enabled {
		t.Fatal("address save changed authentication key")
	}
	again, _, err := p.store.resolveAgent(hash, "manual-host", true)
	if err != nil || again != id {
		t.Fatal("address save or reconnect changed route")
	}
	creds, err = p.store.ListClientCredentialsForServerID(id)
	if err != nil || len(creds) != 1 || creds[0].ID != cid {
		t.Fatal("manual authorization was lost")
	}
}

func TestHTTPSRegistrationAPIKeepsLegacyToken(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	_, oldHash := createSharedToken(t, a, session, cid)
	current, _ := p.store.selfRegistration()
	r := httptest.NewRequest("PUT", "/api/settings/agent-registration/address", strings.NewReader(`{"server_url":"https://example.com"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("HTTPS origin rejected: %d", w.Code)
	}
	updated, _ := p.store.selfRegistration()
	if updated.Token != current.Token {
		t.Fatal("equivalent HTTPS origin changed existing token")
	}
	if _, _, err := p.store.resolveAgent(oldHash, "https-host", true); err != nil {
		t.Fatal("existing token rejected")
	}
	body, _ := json.Marshal(map[string]string{"server_url": "https://example.com", "token": current.Token})
	if registrationRequest(a, session, "PUT", string(body)).Code != 200 {
		t.Fatal("HTTPS origin with WSS token rejected")
	}
}
