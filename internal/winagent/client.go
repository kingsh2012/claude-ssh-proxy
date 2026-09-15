package winagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agentwire"
	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Hostname  string `toml:"hostname"`
	ServerURL string `toml:"server_url"`
	ID        int64  `toml:"id"`
	Token     string `toml:"token"`
}

type EventType string

const (
	EventConnected        EventType = "connected"
	EventConnectionFailed EventType = "connection_failed"
	EventDisconnected     EventType = "disconnected"
	EventTaskStarted      EventType = "task_started"
	EventTaskOutput       EventType = "task_output"
	EventTaskCanceled     EventType = "task_canceled"
	EventTaskFinished     EventType = "task_finished"
)

type Event struct {
	Type        EventType
	Reconnected bool
	TaskID      string
	Command     string
	Stream      string
	Data        []byte
	ExitCode    int
	Duration    time.Duration
	Err         error
}

type EventHandler func(Event)

func report(handler EventHandler, event Event) {
	if handler != nil {
		handler(event)
	}
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err != nil {
		return c, err
	}
	if len(data) > 16*1024 {
		return c, errors.New("Agent配置文件不能超过 16 KiB")
	}
	// Accept UTF-8 BOM from Windows editors, plus ordinary UTF-8 and CRLF.
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	d := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		// Parser diagnostics can include source lines containing credentials.
		return c, errors.New("Agent TOML配置无效，请检查UTF-8 编码、字段名、引号及重复字段")
	}
	if err := ValidateConfig(c); err != nil {
		return c, err
	}
	return c, nil
}

func ValidateConfig(c Config) error {
	if c.Hostname != "" && !agentwire.ValidHostname(c.Hostname) {
		return errors.New("-hostname只能包含字母、数字、点、短横线和下划线，且以字母或数字开头，最多 253 字符")
	}
	_, addressErr := agentwire.AgentServerURL(c.ServerURL)
	if addressErr != nil || c.ID < 0 || len(c.Token) != 64 {
		return errors.New("需要有效HTTPS主域名或WSS地址及设备凭证")
	}
	return nil
}

// Run reconnects but never replays a command. TLS trusts system roots and the embedded CA.
func Run(ctx context.Context, c Config, onEvent EventHandler) {
	run(ctx, c, onEvent, 5*time.Second)
}

func run(ctx context.Context, c Config, onEvent EventHandler, reconnectDelay time.Duration) {
	attempted := false
	for {
		connected, err := connect(ctx, c, onEvent, attempted)
		if ctx.Err() != nil {
			return
		}
		if connected {
			report(onEvent, Event{Type: EventDisconnected, Err: err})
		} else {
			report(onEvent, Event{Type: EventConnectionFailed, Err: err})
		}
		attempted = true
		timer := time.NewTimer(reconnectDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func Connect(ctx context.Context, c Config) error {
	_, err := connect(ctx, c, nil, false)
	return err
}

func connect(ctx context.Context, c Config, onEvent EventHandler, reconnected bool) (bool, error) {
	u, err := url.Parse(c.ServerURL)
	if err == nil && u.Scheme == "https" {
		address, addressErr := agentwire.AgentServerURL(c.ServerURL)
		if addressErr != nil {
			return false, addressErr
		}
		u, err = url.Parse(address)
	}
	if err != nil {
		return false, err
	}
	q := u.Query()
	if c.ID > 0 {
		q.Set("id", strconv.FormatInt(c.ID, 10))
	}
	hostname := c.Hostname
	if hostname == "" {
		hostname, err = os.Hostname()
		if err != nil {
			return false, errors.New("无法读取本机主机名")
		}
	}
	if !agentwire.ValidHostname(hostname) {
		return false, errors.New("代理登录名无效，请使用 -hostname指定")
	}
	q.Set("hostname", hostname)
	u.RawQuery = q.Encode()
	tlsConfig, err := clientTLSConfig(embeddedCABase64)
	if err != nil {
		return false, err
	}
	d := websocket.Dialer{TLSClientConfig: tlsConfig, HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment, Subprotocols: []string{"claude-agent-v1"}}
	raw, resp, err := d.DialContext(ctx, u.String(), http.Header{"Authorization": []string{"Bearer " + c.Token}})
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return false, errors.New("连接失败，请检查地址、证书和设备凭证")
	}
	report(onEvent, Event{Type: EventConnected, Reconnected: reconnected})
	conn := agentwire.Wrap(raw)
	defer conn.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { <-ctx.Done(); conn.Close() }()
	go conn.Heartbeat(ctx.Done())
	var mu sync.Mutex
	var jobCancel context.CancelFunc
	var jobID string
	var wg sync.WaitGroup
	defer func() {
		cancel()
		mu.Lock()
		if jobCancel != nil {
			jobCancel()
		}
		mu.Unlock()
		wg.Wait()
	}()
	for {
		var m agentwire.Message
		if err := conn.ReadJSON(&m); err != nil {
			return true, errors.New("连接已关闭")
		}
		switch m.Type {
		case "exec":
			if m.ID == "" || m.Command == "" || len(m.Command) > agentwire.MaxCommand {
				return true, errors.New("收到无效任务")
			}
			mu.Lock()
			if jobCancel != nil {
				mu.Unlock()
				return true, errors.New("不支持并发任务")
			}
			jobCtx, stop := context.WithTimeout(ctx, agentwire.TaskTimeout)
			jobCancel, jobID = stop, m.ID
			mu.Unlock()
			report(onEvent, Event{Type: EventTaskStarted, TaskID: m.ID, Command: m.Command})
			wg.Add(1)
			go func(m agentwire.Message) {
				started := time.Now()
				defer wg.Done()
				defer stop()
				output := &taskOutput{conn: conn, id: m.ID, cancel: stop, onEvent: onEvent}
				code, err := runPowerShell(jobCtx, m.Command, streamWriter{output, "stdout"}, streamWriter{output, "stderr"})
				if err != nil {
					streamWriter{output, "stderr"}.Write([]byte(fmt.Sprintf("\nTask ended: %v\n", err)))
				}
				if errors.Is(jobCtx.Err(), context.DeadlineExceeded) {
					code = 124
				} else if jobCtx.Err() != nil {
					code = 130
				}
				output.mu.Lock()
				if output.exceeded {
					code = 125
				}
				output.mu.Unlock()
				// Hold the lock through exit transmission: the next exec cannot overtake cleanup.
				mu.Lock()
				jobCancel = nil
				jobID = ""
				if conn.Send(agentwire.Message{Type: "exit", ID: m.ID, Code: code}) != nil {
					conn.Close()
				}
				mu.Unlock()
				report(onEvent, Event{Type: EventTaskFinished, TaskID: m.ID, ExitCode: code, Duration: time.Since(started)})
			}(m)
		case "cancel":
			canceled := false
			mu.Lock()
			if jobID == m.ID && jobCancel != nil {
				jobCancel()
				canceled = true
			}
			mu.Unlock()
			if canceled {
				report(onEvent, Event{Type: EventTaskCanceled, TaskID: m.ID})
			}
		default:
			return true, errors.New("收到不支持的Agent消息")
		}
	}
}

type taskOutput struct {
	mu       sync.Mutex
	conn     *agentwire.Conn
	id       string
	total    int
	exceeded bool
	cancel   context.CancelFunc
	onEvent  EventHandler
}
type streamWriter struct {
	output *taskOutput
	kind   string
}

func (w streamWriter) Write(data []byte) (int, error) {
	o := w.output
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.total+len(data) > agentwire.MaxOutput {
		o.exceeded = true
		o.cancel()
		return 0, errors.New("output exceeded 8 MiB")
	}
	total := len(data)
	for len(data) > 0 {
		n := min(len(data), 8*1024)
		chunk := append([]byte(nil), data[:n]...)
		err := o.conn.Send(agentwire.Message{Type: w.kind, ID: o.id, Data: chunk})
		report(o.onEvent, Event{Type: EventTaskOutput, TaskID: o.id, Stream: w.kind, Data: chunk})
		if err != nil {
			o.cancel()
			o.conn.Close()
			return 0, err
		}
		data = data[n:]
		o.total += n
	}
	return total, nil
}
