package winagent

import (
	"crypto/tls"
	"encoding/base64"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agenttls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEmbeddedCAValidatesIPAndRejectsWrongIdentity(t *testing.T) {
	dir := t.TempDir()
	caDir := filepath.Join(dir, "ca")
	out := filepath.Join(dir, "server")
	if err := agenttls.Generate(caDir, out, []net.IP{net.ParseIP("127.0.0.1")}, nil); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(out, "server.crt"), filepath.Join(out, "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := os.ReadFile(filepath.Join(caDir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()
	for _, tc := range []struct {
		name, ca, serverName string
		ok                   bool
	}{
		{"embedded-ca", base64.StdEncoding.EncodeToString(caPEM), "", true},
		{"system-only", "", "", false},
		{"wrong-ip", base64.StdEncoding.EncodeToString(caPEM), "192.0.2.7", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := clientTLSConfig(tc.ca)
			if err != nil {
				t.Fatal(err)
			}
			cfg.ServerName = tc.serverName
			transport := &http.Transport{TLSClientConfig: cfg}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			resp, err := client.Get(srv.URL)
			if resp != nil {
				resp.Body.Close()
			}
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected TLS result: %v", err)
			}
		})
	}
	for _, value := range []string{"not-base64", base64.StdEncoding.EncodeToString([]byte("not-a-certificate"))} {
		if _, err := clientTLSConfig(value); err == nil {
			t.Fatal("malformed embedded CA silently ignored")
		}
	}
	executable := filepath.Join(dir, "renamed-client.exe")
	if err := os.WriteFile(clientCAPath(executable), caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientTLSConfigForExecutable("", executable)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: cfg}
	defer transport.CloseIdleConnections()
	resp, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(srv.URL)
	if err != nil {
		t.Fatalf("外部CA未被客户端加载：%v", err)
	}
	resp.Body.Close()

	originalEmbeddedCA := embeddedCABase64
	embeddedCABase64 = base64.StdEncoding.EncodeToString(caPEM)
	defer func() { embeddedCABase64 = originalEmbeddedCA }()
	preservedExecutable := filepath.Join(dir, "preserved-client.exe")
	if err := preserveEmbeddedCA(preservedExecutable); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(clientCAPath(preservedExecutable))
	if err != nil {
		t.Fatal(err)
	}
	if string(preserved) != string(caPEM) {
		t.Fatal("升级时保存的CA与内置CA不一致")
	}
}
