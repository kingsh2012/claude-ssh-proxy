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
	rows, err := s.db.Query(`SELECT client_credential_id FROM agent_registration_credentials ORDER BY client_credential_id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return out, err
		}
		out.Credentials = append(out.Credentials, id)
	}
	return out, rows.Err()
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

func (a *API) handlePutSelfRegistration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerURL   string  `json:"server_url"`
		Credentials []int64 `json:"client_credential_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !agentwire.ValidServerURL(body.ServerURL) || len(body.Credentials) == 0 || len(body.Credentials) > 64 {
		writeError(w, 400, "请填写有效 WSS 地址，并选择新主机的默认客户端凭据")
		return
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		writeError(w, 500, "生成 Token 失败")
		return
	}
	secret := hex.EncodeToString(key)
	hash := sha256.Sum256([]byte(secret))
	token := agentwire.EnrollmentToken(body.ServerURL, secret)
	encrypted, err := a.store.encryptSecret(token)
	if err != nil {
		writeError(w, 500, "保存 Token 失败")
		return
	}
	// Use the same lock as WebSocket registration so a rotation cannot admit an old peer.
	h := a.proxy.agents
	h.mu.Lock()
	defer h.mu.Unlock()
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
	for _, id := range body.Credentials {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO agent_registration_credentials(client_credential_id) VALUES(?)`, id); err != nil {
			writeError(w, 400, "客户端凭据不存在")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "保存失败")
		return
	}
	h.disconnectRegisteredLocked()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, SelfRegistration{Enabled: true, ServerURL: body.ServerURL, Token: token, Credentials: body.Credentials})
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
	if err = tx.QueryRow(`SELECT count(*) FROM agent_registration_credentials`).Scan(&count); err != nil {
		return 0, "", err
	}
	if count == 0 {
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
	if _, err = tx.Exec(`INSERT INTO server_client_credentials(server_id,client_credential_id) SELECT ?,client_credential_id FROM agent_registration_credentials`, id); err != nil {
		return 0, "", err
	}
	return id, peerHash, tx.Commit()
}
