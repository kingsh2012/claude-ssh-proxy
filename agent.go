package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agentwire"
	"golang.org/x/crypto/ssh"
)

type AgentHub struct {
	store *Store
	mu    sync.Mutex
	peers map[int64]*agentPeer
}

type agentPeer struct {
	conn   *agentwire.Conn
	hash   string
	done   chan struct{}
	mu     sync.Mutex
	jobID  string
	events chan agentwire.Message
}

func NewAgentHub(s *Store) *AgentHub { return &AgentHub{store: s, peers: make(map[int64]*agentPeer)} }

func (h *AgentHub) Online(id int64) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.peers[id] != nil
}

func (h *AgentHub) Disconnect(id int64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.peers[id]; p != nil {
		delete(h.peers, id)
		p.conn.Close()
	}
}

func (h *AgentHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Browser cookies never authenticate an Agent. Resolve its secret directly.
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "Agent authentication failed", 401)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(token) != 64 {
		http.Error(w, "Agent authentication failed", 401)
		return
	}
	hash := sha256.Sum256([]byte(token))
	expected := hex.EncodeToString(hash[:])
	hostname := r.URL.Query().Get("hostname")
	id, _, err := h.store.resolveAgent(expected, hostname, false)
	if err != nil {
		http.Error(w, "Agent authentication failed", 401)
		return
	}
	if legacyID := r.URL.Query().Get("id"); legacyID != "" {
		parsed, parseErr := strconv.ParseInt(legacyID, 10, 64)
		if parseErr != nil || parsed != id || id == 0 {
			http.Error(w, "Agent authentication failed", 401)
			return
		}
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "Browser Agent connections are not supported", http.StatusForbidden)
		return
	}
	c, err := (&websocket.Upgrader{HandshakeTimeout: 10 * time.Second, Subprotocols: []string{"claude-agent-v1"}}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	peer := &agentPeer{conn: agentwire.Wrap(c), hash: expected, done: make(chan struct{})}
	h.mu.Lock()
	// Recheck revocation and claim a pending token under the connection lock.
	var peerHash string
	id, peerHash, err = h.store.resolveAgent(expected, hostname, true)
	if err != nil || h.peers[id] != nil {
		h.mu.Unlock()
		c.Close()
		return
	}
	peer.hash = peerHash
	h.peers[id] = peer
	h.mu.Unlock()
	defer func() {
		c.Close()
		close(peer.done)
		h.mu.Lock()
		if h.peers[id] == peer {
			delete(h.peers, id)
		}
		h.mu.Unlock()
	}()
	go peer.conn.Heartbeat(peer.done)
	for {
		var m agentwire.Message
		if peer.conn.ReadJSON(&m) != nil {
			return
		}
		if m.Type != "stdout" && m.Type != "stderr" && m.Type != "exit" {
			return
		}
		if len(m.Data) > 16*1024 || m.Code < 0 {
			return
		}
		peer.mu.Lock()
		if m.ID != peer.jobID || peer.events == nil {
			peer.mu.Unlock()
			return
		}
		events := peer.events
		peer.mu.Unlock()
		timer := time.NewTimer(10 * time.Second)
		select {
		case events <- m:
			timer.Stop()
		case <-timer.C:
			return // Bounded backpressure: disconnect stalled consumers.
		}
	}
}

func (a *API) handleRotateAgentToken(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.GetServer(r.PathValue("user"))
	if err != nil || s.ConnectionType != "agent" {
		writeError(w, 400, "请选择 Agent 服务器")
		return
	}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		writeError(w, 500, "生成凭证失败")
		return
	}
	token := hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	if _, err = a.store.db.Exec(`UPDATE servers SET agent_token_hash = ? WHERE id = ?`, hex.EncodeToString(hash[:]), s.ID); err != nil {
		writeError(w, 500, "保存凭证失败")
		return
	}
	a.proxy.agents.Disconnect(s.ID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"id": s.ID, "token": token})
}

func (p *Proxy) handleAgentSSH(conn *ssh.ServerConn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, s ServerRecord, remote, label string) {
	go ssh.DiscardRequests(reqs)
	id := p.addConnection(s, remote, label)
	defer p.removeConnection(id)
	var wg sync.WaitGroup
	for ch := range chans {
		if ch.ChannelType() != "session" {
			ch.Reject(ssh.UnknownChannelType, "Agent supports exec sessions only")
			continue
		}
		c, r, err := ch.Accept()
		if err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.changeActiveSessions(id, 1)
			defer p.changeActiveSessions(id, -1)
			p.agentSession(c, r, s, remote, label)
		}()
	}
	wg.Wait()
}

func (p *Proxy) agentSession(c ssh.Channel, requests <-chan *ssh.Request, s ServerRecord, remote, label string) {
	defer c.Close()
	// Reject interactive sessions and stdin. Only an SSH exec request launches a job.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	var req *ssh.Request
	for req == nil {
		select {
		case <-timer.C:
			return
		case r, ok := <-requests:
			if !ok {
				return
			}
			if r.Type != "exec" {
				if r.WantReply {
					r.Reply(false, nil)
				}
				continue
			}
			req = r
		}
	}
	command, ok := parseSSHString(req.Payload)
	if !ok || command == "" || len(command) > agentwire.MaxCommand {
		req.Reply(false, nil)
		return
	}
	audit := newAuditSession(p.store, s.ProxyUser, remote, s.TargetHost, s.TargetPort, label)
	audit.noteRequest(req)
	defer audit.finish()
	req.Reply(true, nil)
	exit := func(code int, msg string) {
		if msg != "" {
			io.WriteString(io.MultiWriter(c.Stderr(), outputWriter{audit}), msg+"\n")
		}
		payload := ssh.Marshal(struct{ Code uint32 }{uint32(code)})
		audit.noteRequest(&ssh.Request{Type: "exit-status", Payload: payload})
		c.SendRequest("exit-status", false, payload)
	}
	current, err := p.store.GetServer(s.ProxyUser)
	if err != nil || !current.Enabled || current.ConnectionType != "agent" {
		exit(125, "Agent route unavailable")
		return
	}
	p.agents.mu.Lock()
	peer := p.agents.peers[s.ID]
	p.agents.mu.Unlock()
	if peer == nil || peer.hash != current.AgentTokenHash {
		exit(125, "Agent offline")
		return
	}
	jobID := randomAgentID()
	peer.mu.Lock()
	if peer.events != nil {
		peer.mu.Unlock()
		exit(125, "Agent busy; retry after the current task finishes")
		return
	}
	events := make(chan agentwire.Message, 128)
	peer.jobID, peer.events = jobID, events
	peer.mu.Unlock()
	completed := false
	defer func() {
		if !completed {
			peer.conn.Send(agentwire.Message{Type: "cancel", ID: jobID})
			peer.conn.Close()
		}
		peer.mu.Lock()
		if peer.jobID == jobID {
			peer.events = nil
			peer.jobID = ""
		}
		peer.mu.Unlock()
	}()
	if peer.conn.Send(agentwire.Message{Type: "exec", ID: jobID, Command: command}) != nil {
		exit(125, "Agent disconnected before dispatch acknowledgement; task was not retried")
		return
	}
	cancel := make(chan struct{})
	go func() {
		defer close(cancel)
		for r := range requests {
			if r.Type == "signal" {
				if r.WantReply {
					r.Reply(true, nil)
				}
				return
			}
			if r.WantReply {
				r.Reply(false, nil)
			}
		}
	}()
	deadline := time.NewTimer(agentwire.TaskTimeout + 5*time.Second)
	defer deadline.Stop()
	// SSH clients may stop reading. Closing the channel independently unblocks
	// a pending output Write, so cancellation and task limits still take effect.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		t := time.NewTimer(agentwire.TaskTimeout + 6*time.Second)
		defer t.Stop()
		select {
		case <-watchDone:
			return
		case <-cancel:
		case <-peer.done:
		case <-t.C:
		}
		c.Close()
	}()
	total := 0
	for {
		select {
		case <-cancel:
			exit(130, "Task cancelled")
			return
		case <-peer.done:
			exit(125, "Agent disconnected; task outcome may be unknown, no automatic retry")
			return
		case <-deadline.C:
			exit(124, "Task timed out")
			return
		case m := <-events:
			if m.Type == "exit" {
				completed = true
				peer.mu.Lock()
				peer.events = nil
				peer.jobID = ""
				peer.mu.Unlock()
				exit(m.Code, "")
				return
			}
			total += len(m.Data)
			if total > agentwire.MaxOutput {
				exit(125, "Task output exceeded 8 MiB")
				return
			}
			var out io.Writer = c
			if m.Type == "stderr" {
				out = c.Stderr()
			}
			if _, err := io.MultiWriter(out, outputWriter{audit}).Write(m.Data); err != nil {
				return
			}
		}
	}
}

func randomAgentID() string { return fmt.Sprintf("%x", rand.Text()) }
