package winagent

import (
	"context"
	"github.com/pelletier/go-toml/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostnameOverrideAndDefaultAreSent(t *testing.T) {
	localName, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "es-windows-01"} {
		got := make(chan string, 1)
		h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got <- r.URL.Query().Get("hostname")
			http.Error(w, "test rejection", 401)
		}))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		Connect(ctx, Config{ServerURL: "ws" + strings.TrimPrefix(h.URL, "http") + "/agent", Token: strings.Repeat("a", 64), Hostname: name})
		cancel()
		h.Close()
		want := name
		if want == "" {
			want = localName
		}
		select {
		case actual := <-got:
			if actual != want {
				t.Fatal("hostname override/default was not sent")
			}
		default:
			t.Fatal("no request received")
		}
	}
	for _, name := range []string{"bad/name", "bad name", "-bad", strings.Repeat("x", 254)} {
		if ValidateConfig(Config{ServerURL: "wss://example.com/agent", Token: strings.Repeat("a", 64), Hostname: name}) == nil {
			t.Fatal("invalid hostname accepted")
		}
	}
}

func TestConfigRequiresTLSAndSeparateDeviceCredentials(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"wss://proxy.example.com/agent", true},
		{"ws://proxy.example.com/agent", false},
		{"wss://user:password@proxy.example.com/agent", false},
		{"wss://proxy.example.com/agent?token=secret", false},
		{"wss://proxy.example.com/other", false},
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		data, _ := toml.Marshal(Config{ServerURL: tc.url, ID: 1, Token: strings.Repeat("a", 64)})
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadConfig(path)
		if (err == nil) != tc.valid {
			t.Fatalf("TLS/config validation mismatch: valid=%v err=%v", tc.valid, err)
		}
	}
}

func TestTOMLWindowsFormattingAndInvalidConfig(t *testing.T) {
	valid := "# 设备配置\r\nserver_url = 'wss://proxy.example.com/agent'\r\nid = 1\r\ntoken = '" + strings.Repeat("a", 64) + "'\r\n"
	for _, tc := range []struct {
		name, text string
		valid      bool
	}{
		{"windows-crlf", valid, true},
		{"windows-bom", "\xef\xbb\xbf" + valid, true},
		{"duplicate", valid + "id = 2\n", false},
		{"unknown-field", valid + "tokne = 'secret-marker'\n", false},
		{"malformed", valid + "secret-marker = '\n", false},
		{"wrong-type", strings.Replace(valid, "id = 1", "id = '1'", 1), false},
		{"too-large", valid + "#" + strings.Repeat("x", 16*1024), false},
		{"old-json", `{"server_url":"wss://proxy.example.com/agent","id":1,"token":"secret-marker"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent-config.toml")
			if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := LoadConfig(path)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected validation result: %v", err)
			}
			if tc.valid && (c.ID != 1 || c.ServerURL != "wss://proxy.example.com/agent" || len(c.Token) != 64) {
				t.Fatal("TOML fields not loaded correctly")
			}
			if err != nil && strings.Contains(err.Error(), "secret-marker") {
				t.Fatal("parser error leaked configuration content")
			}
		})
	}
}
