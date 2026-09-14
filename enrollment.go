package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/kingsh2012/ops-ssh-proxy/internal/agentwire"
)

var validAgentHostname = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
var errAgentIdentity = errors.New("Agent token is invalid, revoked or already used by another device")

type Enrollment struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	Enabled   bool   `json:"enabled"`
	Used      bool   `json:"used"`
	Hostname  string `json:"hostname"`
	ProxyUser string `json:"proxy_user"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) migrateEnrollments() error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS agent_enrollments (
		 id INTEGER PRIMARY KEY AUTOINCREMENT, label TEXT NOT NULL,
		 token_hash TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1,
		 used INTEGER NOT NULL DEFAULT 0, hostname TEXT NOT NULL DEFAULT '',
		 server_id INTEGER REFERENCES servers(id) ON DELETE SET NULL,
		 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS agent_enrollment_credentials (
		 enrollment_id INTEGER NOT NULL REFERENCES agent_enrollments(id) ON DELETE CASCADE,
		 client_credential_id INTEGER NOT NULL REFERENCES client_credentials(id) ON DELETE CASCADE,
		 PRIMARY KEY(enrollment_id,client_credential_id))`,
		`CREATE INDEX IF NOT EXISTS servers_agent_token_lookup ON servers(agent_token_hash) WHERE agent_token_hash <> ''`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return s.migrateSelfRegistration()
}

func (a *API) handleCreateEnrollment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label       string  `json:"label"`
		ServerURL   string  `json:"server_url"`
		Credentials []int64 `json:"client_credential_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !agentwire.ValidServerURL(body.ServerURL) || len(body.Label) > 120 || len(body.Credentials) == 0 || len(body.Credentials) > 64 {
		writeError(w, 400, "请填写有效 WSS 地址，并选择至少一份客户端凭证")
		return
	}
	if body.Label == "" {
		body.Label = "Windows Agent"
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		writeError(w, 500, "生成 Token 失败")
		return
	}
	secret := hex.EncodeToString(key)
	hash := sha256.Sum256([]byte(secret))
	tx, err := a.store.db.Begin()
	if err != nil {
		writeError(w, 500, "保存 Token 失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO agent_enrollments(label,token_hash) VALUES(?,?)`, body.Label, hex.EncodeToString(hash[:]))
	if err != nil {
		writeError(w, 500, "保存 Token 失败")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		writeError(w, 500, "保存 Token 失败")
		return
	}
	seen := map[int64]bool{}
	for _, cid := range body.Credentials {
		if seen[cid] {
			continue
		}
		seen[cid] = true
		if _, err = tx.Exec(`INSERT INTO agent_enrollment_credentials(enrollment_id,client_credential_id) VALUES(?,?)`, id, cid); err != nil {
			writeError(w, 400, "选择的客户端凭证不存在")
			return
		}
	}
	if tx.Commit() != nil {
		writeError(w, 500, "保存 Token 失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"id": id, "token": agentwire.EnrollmentToken(body.ServerURL, secret)})
}

func (a *API) handleListEnrollments(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.Query(`SELECT e.id,e.label,e.enabled,e.used,e.hostname,COALESCE(s.proxy_user,''),e.created_at FROM agent_enrollments e LEFT JOIN servers s ON s.id=e.server_id ORDER BY e.id DESC`)
	if err != nil {
		writeError(w, 500, "读取接入 Token 失败")
		return
	}
	defer rows.Close()
	out := []Enrollment{}
	for rows.Next() {
		var e Enrollment
		if rows.Scan(&e.ID, &e.Label, &e.Enabled, &e.Used, &e.Hostname, &e.ProxyUser, &e.CreatedAt) != nil {
			writeError(w, 500, "读取接入 Token 失败")
			return
		}
		out = append(out, e)
	}
	if rows.Err() != nil {
		writeError(w, 500, "读取接入 Token 失败")
		return
	}
	writeJSON(w, out)
}

func (a *API) handleRevokeEnrollment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, 400, "无效的 Token 编号")
		return
	}
	tx, err := a.store.db.Begin()
	if err != nil {
		writeError(w, 500, "撤销失败")
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE agent_enrollments SET enabled=0 WHERE id=?`, id)
	if err != nil {
		writeError(w, 500, "撤销失败")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, 404, "Token 不存在")
		return
	}
	var sid sql.NullInt64
	var hash string
	if tx.QueryRow(`SELECT server_id,token_hash FROM agent_enrollments WHERE id=?`, id).Scan(&sid, &hash) != nil {
		writeError(w, 500, "撤销失败")
		return
	}
	res, err = tx.Exec(`UPDATE servers SET agent_token_hash='' WHERE id=? AND agent_token_hash=?`, sid, hash)
	if err != nil {
		writeError(w, 500, "撤销失败")
		return
	}
	n, _ = res.RowsAffected()
	if tx.Commit() != nil {
		writeError(w, 500, "撤销失败")
		return
	}
	if n > 0 {
		a.proxy.agents.Disconnect(sid.Int64)
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// authenticateAgent looks up the secret directly. A pending token is claimed
// only after a valid WebSocket upgrade. First use creates one server atomically.
func (s *Store) authenticateAgent(hash, hostname string, register bool) (int64, error) {
	var id int64
	var enabled bool
	err := s.db.QueryRow(`SELECT id,enabled FROM servers WHERE agent_token_hash=? AND connection_type='agent'`, hash).Scan(&id, &enabled)
	if err == nil {
		if !enabled {
			return 0, errAgentIdentity
		}
		var enrolledHostname string
		var tokenEnabled bool
		e := s.db.QueryRow(`SELECT hostname,enabled FROM agent_enrollments WHERE token_hash=?`, hash).Scan(&enrolledHostname, &tokenEnabled)
		if e != sql.ErrNoRows && (e != nil || !tokenEnabled || enrolledHostname != hostname) {
			return 0, errAgentIdentity
		}
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	if !validAgentHostname.MatchString(hostname) {
		return 0, errAgentIdentity
	}
	var used bool
	err = s.db.QueryRow(`SELECT enabled,used FROM agent_enrollments WHERE token_hash=?`, hash).Scan(&enabled, &used)
	if err != nil || !enabled || used {
		return 0, errAgentIdentity
	}
	if !register {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Reserve the write transaction before reading, to serialize concurrent claims.
	res, err := tx.Exec(`UPDATE agent_enrollments SET used=1 WHERE token_hash=? AND enabled=1 AND used=0`, hash)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return 0, errAgentIdentity
	}
	var eid int64
	if err = tx.QueryRow(`SELECT id FROM agent_enrollments WHERE token_hash=?`, hash).Scan(&eid); err != nil {
		return 0, err
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM agent_enrollment_credentials WHERE enrollment_id=?`, eid).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, errAgentIdentity
	}
	name := hostname
	for suffix := 1; ; suffix++ {
		if suffix > 1000 {
			return 0, errors.New("hostname collision limit exceeded")
		}
		if suffix > 1 {
			name = fmt.Sprintf("%s-%d", hostname, suffix)
		}
		if err = tx.QueryRow(`SELECT count(*) FROM servers WHERE proxy_user=? COLLATE NOCASE`, name).Scan(&count); err != nil {
			return 0, err
		}
		if count == 0 {
			break
		}
	}
	res, err = tx.Exec(`INSERT INTO servers(proxy_user,target_host,target_port,connection_type,agent_token_hash) VALUES(?,?,0,'agent',?)`, name, hostname, hash)
	if err != nil {
		return 0, err
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`INSERT INTO server_client_credentials(server_id,client_credential_id) SELECT ?,client_credential_id FROM agent_enrollment_credentials WHERE enrollment_id=?`, id, eid); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`UPDATE agent_enrollments SET server_id=?,hostname=? WHERE id=?`, id, hostname, eid); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
