package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/kingsh2012/ops-ssh-proxy/internal/agentwire"
)

type SelfRegistration struct {
	Enabled     bool    `json:"enabled"`
	ServerURL   string  `json:"server_url"`
	Token       string  `json:"token"`
	Credentials []int64 `json:"client_credential_ids"`
}

func (s *Store) migrateSelfRegistration() error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS agent_registration_settings (id INTEGER PRIMARY KEY CHECK(id=1), enabled INTEGER NOT NULL, server_url TEXT NOT NULL, token_hash TEXT NOT NULL, token_encrypted TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS agent_registration_credentials (client_credential_id INTEGER PRIMARY KEY REFERENCES client_credentials(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS agent_registration_hosts (name TEXT PRIMARY KEY COLLATE NOCASE, server_id INTEGER UNIQUE REFERENCES servers(id) ON DELETE SET NULL)`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) selfRegistration() (SelfRegistration, error) {
	out := SelfRegistration{Credentials: []int64{}}
	var encrypted string
	err := s.db.QueryRow(`SELECT enabled,server_url,token_encrypted FROM agent_registration_settings WHERE id=1`).Scan(&out.Enabled, &out.ServerURL, &encrypted)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Token, err = s.decryptSecret(encrypted)
	if err != nil {
		return out, err
	}
	return out, nil
}

func (a *API) handleGetSelfRegistration(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.selfRegistration()
	if err != nil {
		writeError(w, 500, "读取自注册设置失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}

// Saving the destination keeps the authentication secret and existing peers intact.
func (a *API) handleSaveRegistrationAddress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerURL string `json:"server_url"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if address, err := agentwire.AgentServerURL(body.ServerURL); err == nil {
		body.ServerURL = address
	} else {
		writeError(w, 400, "请填写 HTTPS 主域名，例如 https://proxy.example.com")
		return
	}
	h := a.proxy.agents
	h.mu.Lock()
	defer h.mu.Unlock()
	current, err := a.store.selfRegistration()
	if err != nil {
		writeError(w, 500, "读取自注册设置失败")
		return
	}
	current.ServerURL = body.ServerURL
	if current.Token != "" {
		_, secret, err := agentwire.ParseEnrollmentToken(current.Token)
		if err != nil {
			writeError(w, 500, "读取当前密钥失败")
			return
		}
		current.Token = agentwire.EnrollmentToken(body.ServerURL, secret)
	}
	encrypted, err := a.store.encryptSecret(current.Token)
	if err != nil {
		writeError(w, 500, "保存地址失败")
		return
	}
	_, err = a.store.db.Exec(`INSERT INTO agent_registration_settings(id,enabled,server_url,token_hash,token_encrypted) VALUES(1,0,?,'',?) ON CONFLICT(id) DO UPDATE SET server_url=excluded.server_url,token_encrypted=excluded.token_encrypted`, body.ServerURL, encrypted)
	if err != nil {
		writeError(w, 500, "保存地址失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, current)
}

// Generate a draft only; the active key remains unchanged until PUT succeeds.
func (a *API) handleGenerateSelfRegistration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerURL string `json:"server_url"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if address, err := agentwire.AgentServerURL(body.ServerURL); err == nil {
		body.ServerURL = address
	} else {
		writeError(w, 400, "请填写 HTTPS 主域名，例如 https://proxy.example.com")
		return
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		writeError(w, 500, "生成密钥失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]string{"token": agentwire.EnrollmentToken(body.ServerURL, hex.EncodeToString(key))})
}

func (a *API) handlePutSelfRegistration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token     *string `json:"token"`
		ServerURL string  `json:"server_url"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if address, err := agentwire.AgentServerURL(body.ServerURL); err == nil {
		body.ServerURL = address
	} else {
		writeError(w, 400, "请填写 HTTPS 主域名，例如 https://proxy.example.com")
		return
	}
	var token, secret string
	if body.Token != nil {
		var serverURL string
		var err error
		token = *body.Token
		serverURL, secret, err = agentwire.ParseEnrollmentToken(token)
		if err != nil || serverURL != body.ServerURL {
			writeError(w, 400, "密钥无效或连接地址已修改，请重新随机生成密钥")
			return
		}
	} else {
		// Preserve the previous API contract for older management clients.
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			writeError(w, 500, "生成密钥失败")
			return
		}
		secret = hex.EncodeToString(key)
		token = agentwire.EnrollmentToken(body.ServerURL, secret)
	}
	hash := sha256.Sum256([]byte(secret))
	encrypted, err := a.store.encryptSecret(token)
	if err != nil {
		writeError(w, 500, "保存 Token 失败")
		return
	}
	// Use the same lock as WebSocket registration so a rotation cannot admit an old peer.
	h := a.proxy.agents
	h.mu.Lock()
	defer h.mu.Unlock()
	var previousHash string
	err = a.store.db.QueryRow(`SELECT token_hash FROM agent_registration_settings WHERE id=1`).Scan(&previousHash)
	if err != nil && err != sql.ErrNoRows {
		writeError(w, 500, "读取原密钥失败")
		return
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		writeError(w, 500, "保存失败")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO agent_registration_settings(id,enabled,server_url,token_hash,token_encrypted) VALUES(1,1,?,?,?) ON CONFLICT(id) DO UPDATE SET enabled=1,server_url=excluded.server_url,token_hash=excluded.token_hash,token_encrypted=excluded.token_encrypted`, body.ServerURL, hex.EncodeToString(hash[:]), encrypted)
	if err != nil {
		writeError(w, 500, "保存失败")
		return
	}
	if _, err = tx.Exec(`DELETE FROM agent_registration_credentials`); err != nil {
		writeError(w, 500, "保存失败")
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "保存失败")
		return
	}
	if previousHash != hex.EncodeToString(hash[:]) {
		h.disconnectRegisteredLocked()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, SelfRegistration{Enabled: true, ServerURL: body.ServerURL, Token: token, Credentials: []int64{}})
}

func (a *API) handleDisableSelfRegistration(w http.ResponseWriter, r *http.Request) {
	h := a.proxy.agents
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := a.store.db.Exec(`UPDATE agent_registration_settings SET enabled=0 WHERE id=1`); err != nil {
		writeError(w, 500, "停用失败")
		return
	}
	h.disconnectRegisteredLocked()
	writeJSON(w, map[string]bool{"ok": true})
}

// Caller holds h.mu. Closing the socket interrupts jobs and prevents further use.
func (h *AgentHub) disconnectRegisteredLocked() {
	rows, err := h.store.db.Query(`SELECT server_id FROM agent_registration_hosts WHERE server_id IS NOT NULL`)
	if err != nil { // Fail closed if membership cannot be read.
		for id, p := range h.peers {
			p.conn.Close()
			delete(h.peers, id)
		}
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			if p := h.peers[id]; p != nil {
				p.conn.Close()
				delete(h.peers, id)
			}
		}
	}
}

// Each shared-key route has a derived hash, so legacy token lookup cannot bypass
// shared-key revocation. Names identify routes; possessing the shared key grants
// registration/reconnection authority for those routes.
func (s *Store) resolveAgent(hash, hostname string, register bool) (int64, string, error) {
	var configured string
	var enabled bool
	err := s.db.QueryRow(`SELECT token_hash,enabled FROM agent_registration_settings WHERE id=1`).Scan(&configured, &enabled)
	if err != nil && err != sql.ErrNoRows {
		return 0, "", err
	}
	if configured != hash {
		id, e := s.authenticateAgent(hash, hostname, register)
		return id, hash, e
	}
	if !enabled || !agentwire.ValidHostname(hostname) {
		return 0, "", errAgentIdentity
	}
	derived := sha256.Sum256([]byte("ops-shared-agent\x00" + hash + "\x00" + strings.ToLower(hostname)))
	peerHash := hex.EncodeToString(derived[:])
	if !register {
		return 0, peerHash, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE agent_registration_settings SET enabled=enabled WHERE id=1 AND enabled=1 AND token_hash=?`, hash)
	if err != nil {
		return 0, "", err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return 0, "", errAgentIdentity
	}
	var sid sql.NullInt64
	err = tx.QueryRow(`SELECT server_id FROM agent_registration_hosts WHERE name=?`, hostname).Scan(&sid)
	if err == nil {
		if !sid.Valid {
			return 0, "", errAgentIdentity
		}
		res, err = tx.Exec(`UPDATE servers SET agent_token_hash=? WHERE id=? AND enabled=1 AND connection_type='agent' AND proxy_user=? COLLATE NOCASE`, peerHash, sid.Int64, hostname)
		if err != nil {
			return 0, "", err
		}
		n, _ = res.RowsAffected()
		if n != 1 {
			return 0, "", errAgentIdentity
		}
		return sid.Int64, peerHash, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return 0, "", err
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM servers WHERE proxy_user=? COLLATE NOCASE`, hostname).Scan(&count); err != nil {
		return 0, "", err
	}
	if count != 0 {
		return 0, "", errAgentIdentity
	}
	res, err = tx.Exec(`INSERT INTO servers(proxy_user,target_host,target_port,connection_type,agent_token_hash) VALUES(?,?,0,'agent',?)`, hostname, hostname, peerHash)
	if err != nil {
		return 0, "", err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, "", err
	}
	if _, err = tx.Exec(`INSERT INTO agent_registration_hosts(name,server_id) VALUES(?,?)`, hostname, id); err != nil {
		return 0, "", err
	}
	return id, peerHash, tx.Commit()
}
