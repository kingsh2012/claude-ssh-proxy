package main

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Listener settings are applied together at startup; saving does not interrupt sessions.
type ListenerSettings struct {
	WebAddr         string `json:"web_listen_addr"`
	AgentAddr       string `json:"agent_listen_addr"`
	AgentTLSEnabled bool   `json:"agent_tls_enabled"`
	AgentCertFile   string `json:"agent_tls_cert_file"`
	AgentKeyFile    string `json:"agent_tls_key_file"`
}

func (s *Store) listenerSettings() ListenerSettings {
	return ListenerSettings{
		WebAddr:         s.GetSetting("web_listen_addr", "127.0.0.1:8080"),
		AgentAddr:       s.GetSetting("agent_listen_addr", "127.0.0.1:8081"),
		AgentTLSEnabled: s.GetSetting("agent_tls_enabled", "false") == "true",
		AgentCertFile:   s.GetSetting("agent_tls_cert_file", ""),
		AgentKeyFile:    s.GetSetting("agent_tls_key_file", ""),
	}
}

func validListenAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	n, portErr := strconv.Atoi(port)
	return err == nil && portErr == nil && n > 0 && n <= 65535 && strings.TrimSpace(value) == value && !strings.ContainsAny(host, " /\\?#@")
}

func (s ListenerSettings) validate() error {
	if !validListenAddress(s.WebAddr) || !validListenAddress(s.AgentAddr) {
		return errors.New("请填写有效的Web和Agent监听地址，例如127.0.0.1:8080或:8443")
	}
	if s.WebAddr == s.AgentAddr {
		return errors.New("Web和Agent不能使用同一个监听地址")
	}
	if s.AgentTLSEnabled {
		if _, err := s.agentTLSConfig(); err != nil {
			return err
		}
	}
	return nil
}

func (s ListenerSettings) agentTLSConfig() (*tls.Config, error) {
	if !s.AgentTLSEnabled {
		return nil, nil
	}
	if s.AgentCertFile == "" || s.AgentKeyFile == "" {
		return nil, errors.New("启用Agent内置TLS时必须填写证书和私钥文件路径")
	}
	cert, err := tls.LoadX509KeyPair(s.AgentCertFile, s.AgentKeyFile)
	if err != nil {
		// Do not return certificate/private-key contents in API diagnostics.
		return nil, errors.New("无法加载Agent证书与私钥，请检查文件权限、PEM格式及是否匹配")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, nil
}

func (s *Store) saveListenerSettings(value ListenerSettings) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, val := range map[string]string{
		"web_listen_addr": value.WebAddr, "agent_listen_addr": value.AgentAddr,
		"agent_tls_enabled":   strconv.FormatBool(value.AgentTLSEnabled),
		"agent_tls_cert_file": value.AgentCertFile, "agent_tls_key_file": value.AgentKeyFile,
	} {
		if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, val); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *API) handleGetListeners(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.store.listenerSettings())
}

func (a *API) handlePutListeners(w http.ResponseWriter, r *http.Request) {
	var value ListenerSettings
	if !decodeJSON(w, r, &value) {
		return
	}
	if err := value.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.saveListenerSettings(value); err != nil {
		writeError(w, http.StatusInternalServerError, "保存监听设置失败")
		return
	}
	writeJSON(w, map[string]bool{"ok": true, "restart_required": true})
}

func webRouter(api http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	// Explicitly reject Agent traffic instead of falling back to the SPA.
	mux.HandleFunc("/agent", http.NotFound)
	mux.HandleFunc("/agent/", http.NotFound)
	mux.Handle("/", webUIHandler())
	return mux
}

func agentRouter(agent http.Handler) http.Handler {
	// Avoid mux path cleaning/redirects: this public listener has exactly one route.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		agent.ServeHTTP(w, r)
	})
}

type listenerPair struct {
	web, agent             net.Listener
	webServer, agentServer *http.Server
}

// Bind both sockets before serving anything. Failure leaves neither exposed.
func openListeners(settings ListenerSettings, web, agent http.Handler) (*listenerPair, error) {
	if err := settings.validate(); err != nil {
		return nil, err
	}
	tlsConfig, err := settings.agentTLSConfig()
	if err != nil {
		return nil, err
	}
	webListener, err := net.Listen("tcp", settings.WebAddr)
	if err != nil {
		return nil, err
	}
	agentListener, err := net.Listen("tcp", settings.AgentAddr)
	if err != nil {
		webListener.Close()
		return nil, err
	}
	if tlsConfig != nil {
		agentListener = tls.NewListener(agentListener, tlsConfig)
	}
	server := func(handler http.Handler) *http.Server {
		return &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10}
	}
	return &listenerPair{web: webListener, agent: agentListener, webServer: server(web), agentServer: server(agent)}, nil
}

func (p *listenerPair) Close() {
	p.webServer.Close()
	p.agentServer.Close()
	p.web.Close()
	p.agent.Close()
}

func (p *listenerPair) Serve() error {
	errs := make(chan error, 2)
	go func() { errs <- p.webServer.Serve(p.web) }()
	go func() { errs <- p.agentServer.Serve(p.agent) }()
	err := <-errs
	p.Close()
	return err
}
