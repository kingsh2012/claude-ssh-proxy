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
	"github.com/kingsh2012/ops-ssh-proxy/internal/agentwire"
	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Hostname  string `toml:"hostname"`
	ServerURL string `toml:"server_url"`
	ID        int64  `toml:"id"`
	Token     string `toml:"token"`
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
		return c, errors.New("Agent 配置文件不能超过 16 KiB")
	}
	// Accept UTF-8 BOM from Windows editors, plus ordinary UTF-8 and CRLF.
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	d := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		// Parser diagnostics can include source lines containing credentials.
		return c, errors.New("Agent TOML 配置无效，请检查 UTF-8 编码、字段名、引号及重复字段")
	}
	if err := ValidateConfig(c); err != nil {
		return c, err
	}
	return c, nil
}

func ValidateConfig(c Config) error {
	if c.Hostname != "" && !agentwire.ValidHostname(c.Hostname) {
		return errors.New("-hostname 只能包含字母、数字、点、短横线和下划线，且以字母或数字开头，最多 253 字符")
	}
	if !agentwire.ValidServerURL(c.ServerURL) || c.ID < 0 || len(c.Token) != 64 {
		return errors.New("需要有效 WSS 地址及设备凭证")
	}
	return nil
}

// Run reconnects but never replays a command. TLS uses the system trust store.
func Run(ctx context.Context, c Config, onError func(error)) {
	for {
		err := Connect(ctx, c)
		if ctx.Err() != nil {
			return
		}
		if onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func Connect(ctx context.Context, c Config) error {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return err
	}
	q := u.Query()
	if c.ID > 0 {
		q.Set("id", strconv.FormatInt(c.ID, 10))
	}
	hostname := c.Hostname
	if hostname == "" {
		hostname, err = os.Hostname()
		if err != nil {
			return errors.New("无法读取本机主机名")
		}
	}
	if !agentwire.ValidHostname(hostname) {
		return errors.New("代理登录名无效，请使用 -hostname 指定")
	}
	q.Set("hostname", hostname)
	u.RawQuery = q.Encode()
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment, Subprotocols: []string{"claude-agent-v1"}}
	raw, resp, err := d.DialContext(ctx, u.String(), http.Header{"Authorization": []string{"Bearer " + c.Token}})
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return errors.New("Agent connection failed; check address, certificate and device credentials")
	}
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
			return errors.New("Agent connection closed")
		}
		switch m.Type {
		case "exec":
			if m.ID == "" || m.Command == "" || len(m.Command) > agentwire.MaxCommand {
				return errors.New("invalid task")
			}
			mu.Lock()
			if jobCancel != nil {
				mu.Unlock()
				return errors.New("concurrent task rejected")
			}
			jobCtx, stop := context.WithTimeout(ctx, agentwire.TaskTimeout)
			jobCancel, jobID = stop, m.ID
			mu.Unlock()
			wg.Add(1)
			go func(m agentwire.Message) {
				defer wg.Done()
				defer stop()
				output := &taskOutput{conn: conn, id: m.ID, cancel: stop}
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
			}(m)
		case "cancel":
			mu.Lock()
			if jobID == m.ID && jobCancel != nil {
				jobCancel()
			}
			mu.Unlock()
		default:
			return errors.New("unsupported Agent message")
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
		if err := o.conn.Send(agentwire.Message{Type: w.kind, ID: o.id, Data: data[:n]}); err != nil {
			o.cancel()
			o.conn.Close()
			return 0, err
		}
		data = data[n:]
		o.total += n
	}
	return total, nil
}
