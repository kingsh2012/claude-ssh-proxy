package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agenttls"
)

func freeListenerAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestIndependentListenersAndIPTLS(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	secret, _ := createSharedToken(t, a, session, cid)
	dir := t.TempDir()
	caDir := filepath.Join(dir, "ca")
	out := filepath.Join(dir, "server")
	if err := agenttls.Generate(caDir, out, []net.IP{net.ParseIP("127.0.0.1")}, nil); err != nil {
		t.Fatal(err)
	}
	cfg := ListenerSettings{WebAddr: freeListenerAddress(t), AgentAddr: freeListenerAddress(t), AgentTLSEnabled: true, AgentCertFile: filepath.Join(out, "server.crt"), AgentKeyFile: filepath.Join(out, "server.key")}
	pair, err := openListeners(cfg, webRouter(a.Router()), agentRouter(p.agents))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- pair.Serve() }()
	defer func() { pair.Close(); <-done }()
	caPEM, err := os.ReadFile(filepath.Join(caDir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("bad CA")
	}
	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	tr := &http.Transport{TLSClientConfig: tlsConfig}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	for _, tc := range []struct {
		url    string
		status int
	}{
		{"http://" + cfg.WebAddr + "/agent", 404},
		{"http://" + cfg.WebAddr + "/api/me", 401},
		{"https://" + cfg.AgentAddr + "/", 404},
		{"https://" + cfg.AgentAddr + "/api/settings", 404},
		{"https://" + cfg.AgentAddr + "/api/login", 404},
		{"https://" + cfg.AgentAddr + "/agent/", 404},
		{"https://" + cfg.AgentAddr + "/agent", 401},
	} {
		resp, err := client.Get(tc.url)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%s returned %d", tc.url, resp.StatusCode)
		}
	}
	dialer := websocket.Dialer{TLSClientConfig: tlsConfig, HandshakeTimeout: 3 * time.Second, Subprotocols: []string{"claude-agent-v1"}}
	conn, resp, err := dialer.Dial("wss://"+cfg.AgentAddr+"/agent?hostname=ip-tls-host", http.Header{"Authorization": []string{"Bearer " + secret}})
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		host, err := p.store.GetServer("ip-tls-host")
		if err == nil && p.agents.Online(host.ID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("IP WSS self-registration did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	wrong := tlsConfig.Clone()
	wrong.ServerName = "192.0.2.99"
	if c, err := tls.Dial("tcp", cfg.AgentAddr, wrong); err == nil {
		c.Close()
		t.Fatal("wrong IP accepted")
	}
	if c, err := tls.Dial("tcp", cfg.AgentAddr, &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}); err == nil {
		c.Close()
		t.Fatal("unknown CA accepted")
	}
}

func TestListenerSettingsAuthValidationAndPersistence(t *testing.T) {
	p, a, session, _ := enrollmentFixture(t)
	initial := p.store.listenerSettings()
	request := func(value ListenerSettings, cookie string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(value)
		r := httptest.NewRequest("PUT", "/api/settings/listeners", strings.NewReader(string(data)))
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		return w
	}
	if request(initial, "").Code != 401 {
		t.Fatal("unauthenticated settings update accepted")
	}
	for _, addr := range []string{"", ":0", ":65536", "http://localhost:8080", ":http", "localhost", "127.0.0.1: 80"} {
		cfg := initial
		cfg.AgentAddr = addr
		if request(cfg, session).Code != 400 {
			t.Fatalf("invalid address accepted: %q", addr)
		}
	}
	same := initial
	same.AgentAddr = same.WebAddr
	if request(same, session).Code != 400 {
		t.Fatal("same listener accepted")
	}
	broken := initial
	broken.AgentTLSEnabled = true
	broken.AgentCertFile = "missing-cert"
	broken.AgentKeyFile = "private-marker"
	w := request(broken, session)
	if w.Code != 400 || strings.Contains(w.Body.String(), "private-marker") {
		t.Fatal("invalid TLS accepted or private detail leaked")
	}
	if got := p.store.listenerSettings(); got != initial {
		t.Fatal("failed update changed stored settings")
	}
	valid := initial
	valid.AgentAddr = ":8443"
	valid.WebAddr = "192.0.2.10:8080"
	if request(valid, session).Code != 200 {
		t.Fatal("valid settings rejected")
	}
	if got := p.store.listenerSettings(); got != valid {
		t.Fatal("settings not persisted")
	}
	r := httptest.NewRequest("GET", "/api/settings/listeners", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	response := httptest.NewRecorder()
	a.Router().ServeHTTP(response, r)
	var got ListenerSettings
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil || got != valid {
		t.Fatal("saved settings cannot be read")
	}
}

func TestListenerBindFailureClosesOtherSocket(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	cfg := ListenerSettings{WebAddr: freeListenerAddress(t), AgentAddr: occupied.Addr().String()}
	if pair, err := openListeners(cfg, http.NotFoundHandler(), http.NotFoundHandler()); err == nil {
		pair.Close()
		t.Fatal("occupied Agent port accepted")
	}
	released, err := net.Listen("tcp", cfg.WebAddr)
	if err != nil {
		t.Fatal("Web socket leaked after Agent bind failure")
	}
	released.Close()
}
