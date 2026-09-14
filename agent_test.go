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
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agentwire"
	"golang.org/x/crypto/ssh"
)

func agentFixture(t *testing.T) (*Proxy, *ServerRecord, string, *httptest.Server) {
	t.Helper()
	s := openTestStore(t)
	if err := s.UpsertServer(ServerRecord{ProxyUser: "windows-test", ConnectionType: "agent", TargetHost: "Windows Agent"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetServer("windows-test")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	hash := sha256.Sum256([]byte(token))
	if _, err = s.db.Exec(`UPDATE servers SET agent_token_hash = ? WHERE id = ?`, hex.EncodeToString(hash[:]), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateClientCredential(ClientCredential{Label: "test-LLM", AuthType: "password", Password: "test-password"}, []string{r.ProxyUser}); err != nil {
		t.Fatal(err)
	}
	p, err := NewProxy(s, filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.agents.Disconnect(r.ID); p.Stop() })
	h := httptest.NewServer(http.HandlerFunc(p.agents.ServeHTTP))
	t.Cleanup(h.Close)
	return p, r, token, h
}

func dialTestAgent(t *testing.T, p *Proxy, r *ServerRecord, token string, h *httptest.Server) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(h.URL, "http") + fmt.Sprintf("/agent?id=%d", r.ID)
	c, _, err := websocket.DefaultDialer.Dial(u, http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	deadline := time.Now().Add(time.Second)
	for !p.agents.Online(r.ID) {
		if time.Now().After(deadline) {
			t.Fatal("Agent did not register")
		}
		time.Sleep(time.Millisecond)
	}
	return c
}

func dialTestSSH(t *testing.T, p *Proxy) *ssh.Client {
	t.Helper()
	c, err := ssh.Dial("tcp", p.listener.Addr().String(), &ssh.ClientConfig{User: "windows-test", Auth: []ssh.AuthMethod{ssh.Password("test-password")}, HostKeyCallback: ssh.FixedHostKey(p.hostSigner.PublicKey()), Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestAgentSSHExecStreamsExitAndAudit(t *testing.T) {
	p, r, token, h := agentFixture(t)
	c := dialTestAgent(t, p, r, token, h)
	errCh := make(chan error, 1)
	go func() {
		var m agentwire.Message
		if err := c.ReadJSON(&m); err != nil {
			errCh <- err
			return
		}
		if m.Type != "exec" || m.Command != "Get-Content es.log -Tail 20" {
			errCh <- fmt.Errorf("unexpected task: %s", m.Type)
			return
		}
		for _, reply := range []agentwire.Message{{Type: "stdout", ID: m.ID, Data: []byte("中文日志\n")}, {Type: "stderr", ID: m.ID, Data: []byte("error-stream\n")}, {Type: "exit", ID: m.ID, Code: 7}} {
			if err := c.WriteJSON(reply); err != nil {
				errCh <- err
				return
			}
		}
		errCh <- nil
	}()
	client := dialTestSSH(t, p)
	s, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, err := s.CombinedOutput("Get-Content es.log -Tail 20")
	e, ok := err.(*ssh.ExitError)
	if !ok || e.ExitStatus() != 7 || !strings.Contains(string(out), "中文日志") || !strings.Contains(string(out), "error-stream") {
		t.Fatalf("output=%q err=%v", out, err)
	}
	if err = <-errCh; err != nil {
		t.Fatal(err)
	}
	logs, err := p.store.ListAuditLogs(10, AuditFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Command != "Get-Content es.log -Tail 20" || !strings.Contains(logs[0].Output, "error-stream") || logs[0].ExitStatus == nil || *logs[0].ExitStatus != 7 {
		t.Fatalf("unexpected audit: %+v", logs)
	}
}

func TestAgentRejectsBadTokenAndDisabledRoute(t *testing.T) {
	p, r, token, h := agentFixture(t)
	u := "ws" + strings.TrimPrefix(h.URL, "http") + fmt.Sprintf("/agent?id=%d", r.ID)
	for _, bad := range []string{"", strings.Repeat("b", 64)} {
		c, resp, err := websocket.DefaultDialer.Dial(u, http.Header{"Authorization": []string{"Bearer " + bad}})
		if c != nil {
			c.Close()
		}
		if err == nil || resp == nil || resp.StatusCode != 401 {
			t.Fatal("bad Agent credential accepted")
		}
		resp.Body.Close()
	}
	if err := p.store.SetServerEnabled(r.ProxyUser, false); err != nil {
		t.Fatal(err)
	}
	c, resp, err := websocket.DefaultDialer.Dial(u, http.Header{"Authorization": []string{"Bearer " + token}})
	if c != nil {
		c.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatal("disabled Agent accepted")
	}
	resp.Body.Close()
}

func TestAgentOfflineAndInteractiveRejection(t *testing.T) {
	p, _, _, _ := agentFixture(t)
	client := dialTestSSH(t, p)
	s, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
		t.Fatal("PTY unexpectedly accepted")
	}
	if err = s.Shell(); err == nil {
		t.Fatal("shell unexpectedly accepted")
	}
	s.Close()
	s, err = client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, err := s.CombinedOutput("Get-Service")
	e, ok := err.(*ssh.ExitError)
	if !ok || e.ExitStatus() != 125 || !strings.Contains(string(out), "offline") {
		t.Fatalf("output=%q err=%v", out, err)
	}
}

func TestAgentBusyAndSSHDisconnectCancels(t *testing.T) {
	p, r, token, h := agentFixture(t)
	c := dialTestAgent(t, p, r, token, h)
	client := dialTestSSH(t, p)
	s, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Start("Start-Sleep 60"); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var job agentwire.Message
	if err = c.ReadJSON(&job); err != nil {
		t.Fatal(err)
	}
	second, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	out, err := second.CombinedOutput("Get-Service")
	e, ok := err.(*ssh.ExitError)
	if !ok || e.ExitStatus() != 125 || !strings.Contains(string(out), "busy") {
		t.Fatalf("busy rejection failed: %q %v", out, err)
	}
	s.Close()
	var cancellation agentwire.Message
	if err = c.ReadJSON(&cancellation); err != nil {
		t.Fatal(err)
	}
	if cancellation.Type != "cancel" || cancellation.ID != job.ID {
		t.Fatal("SSH disconnect did not cancel original task")
	}
}

func TestAgentTokenRotationAndAPIIsolation(t *testing.T) {
	p, r, old, h := agentFixture(t)
	c := dialTestAgent(t, p, r, old, h)
	a := NewAPI(p.store, p)
	if err := p.store.CreateAdminUser("admin", "test-admin-password"); err != nil {
		t.Fatal(err)
	}
	if err := p.store.SetAdminPassword("admin", "test-admin-password"); err != nil {
		t.Fatal(err)
	}
	session, err := a.issueToken("admin")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/servers/" + r.ProxyUser + "/agent-token"
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, httptest.NewRequest("POST", path, nil))
	if w.Code != 401 {
		t.Fatalf("unauthenticated rotation status=%d", w.Code)
	}
	request := httptest.NewRequest("POST", path, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	w = httptest.NewRecorder()
	a.Router().ServeHTTP(w, request)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rotation status=%d", w.Code)
	}
	var result struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != r.ID || len(result.Token) != 64 || result.Token == old {
		t.Fatal("invalid token rotation")
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = c.ReadMessage(); err == nil {
		t.Fatal("old connection survived rotation")
	}
	request = httptest.NewRequest("GET", "/api/servers", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	w = httptest.NewRecorder()
	a.Router().ServeHTTP(w, request)
	if strings.Contains(w.Body.String(), result.Token) || strings.Contains(w.Body.String(), "agent_token_hash") {
		t.Fatal("server API leaked device credentials")
	}
	u := "ws" + strings.TrimPrefix(h.URL, "http") + fmt.Sprintf("/agent?id=%d", r.ID)
	oldConn, resp, err := websocket.DefaultDialer.Dial(u, http.Header{"Authorization": []string{"Bearer " + old}})
	if oldConn != nil {
		oldConn.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatal("old token still accepted")
	}
	resp.Body.Close()
	dialTestAgent(t, p, r, result.Token, h)
}
