package winagent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agentwire"
	"github.com/pelletier/go-toml/v2"
)

func waitEvent(t *testing.T, events <-chan Event, kind EventType) Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == kind {
				return event
			}
		case <-deadline:
			t.Fatalf("未收到事件%s", kind)
		}
	}
}

func TestRunReportsConnectTaskDisconnectAndReconnect(t *testing.T) {
	events := make(chan Event, 32)
	connections := make(chan int, 4)
	upgrader := websocket.Upgrader{Subprotocols: []string{"claude-agent-v1"}}
	command := "exit 7"
	if runtime.GOOS == "windows" {
		command = "Write-Output 'event-stdout'; [Console]::Error.WriteLine('event-stderr'); exit 7"
	}
	count := 0
	var countMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		countMu.Lock()
		count++
		current := count
		countMu.Unlock()
		connections <- current
		if current == 1 {
			return
		}
		if err := conn.WriteJSON(agentwire.Message{Type: "exec", ID: "task-status-test", Command: command}); err != nil {
			return
		}
		for {
			var message agentwire.Message
			if conn.ReadJSON(&message) != nil {
				return
			}
			if message.Type == "exit" && message.ID == "task-status-test" {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		run(ctx, Config{
			ServerURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/agent",
			Token:     strings.Repeat("a", 64),
			Hostname:  "event-test",
		}, func(event Event) { events <- event }, 10*time.Millisecond)
		close(done)
	}()

	first := waitEvent(t, events, EventConnected)
	if first.Reconnected {
		t.Fatal("首次连接错误标记为重连")
	}
	waitEvent(t, events, EventDisconnected)
	second := waitEvent(t, events, EventConnected)
	if !second.Reconnected {
		t.Fatal("重连成功未标记")
	}
	started := waitEvent(t, events, EventTaskStarted)
	if started.TaskID != "task-status-test" || started.Command != command {
		t.Fatalf("任务开始事件错误：%+v", started)
	}
	var stdout, stderr strings.Builder
	var finished Event
	deadline := time.After(3 * time.Second)
waitForFinish:
	for {
		select {
		case event := <-events:
			switch event.Type {
			case EventTaskOutput:
				if event.Stream == "stdout" {
					stdout.Write(event.Data)
				} else if event.Stream == "stderr" {
					stderr.Write(event.Data)
				}
			case EventTaskFinished:
				finished = event
				break waitForFinish
			}
		case <-deadline:
			t.Fatal("未收到任务结束事件")
		}
	}
	if finished.TaskID != "task-status-test" || finished.Duration <= 0 {
		t.Fatalf("任务结束事件错误：%+v", finished)
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(stdout.String(), "event-stdout") {
			t.Fatalf("标准输出事件缺少任务内容：%q", stdout.String())
		}
		if !strings.Contains(stderr.String(), "event-stderr") {
			t.Fatalf("标准错误事件缺少任务内容：%q", stderr.String())
		}
	} else if !strings.Contains(stderr.String(), "PowerShell Agent requires Windows") {
		t.Fatalf("非Windows执行错误未写入标准错误事件：%q", stderr.String())
	}
	select {
	case firstConnection := <-connections:
		if firstConnection != 1 {
			t.Fatal(fmt.Sprintf("首次连接编号错误：%d", firstConnection))
		}
	default:
		t.Fatal("服务端未收到首次连接")
	}
	select {
	case secondConnection := <-connections:
		if secondConnection != 2 {
			t.Fatal(fmt.Sprintf("重连编号错误：%d", secondConnection))
		}
	default:
		t.Fatal("服务端未收到重连")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("关闭后Run未退出")
	}
}

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
