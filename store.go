package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, fmt.Errorf("设置 WAL 模式失败: %w", err)
	}
	db.SetMaxOpenConns(8)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS admin_users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			initialized INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS routes (
			route_user TEXT PRIMARY KEY,
			target_host TEXT NOT NULL,
			target_port INTEGER NOT NULL DEFAULT 22,
			target_user TEXT NOT NULL,
			auth_type TEXT NOT NULL,
			auth_password TEXT,
			auth_private_key TEXT,
			auth_private_key_passphrase TEXT,
			listen_password_hash TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS client_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			label TEXT NOT NULL,
			public_key TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS route_client_keys (
			route_user TEXT NOT NULL REFERENCES routes(route_user) ON DELETE CASCADE,
			client_key_id INTEGER NOT NULL REFERENCES client_keys(id) ON DELETE CASCADE,
			PRIMARY KEY (route_user, client_key_id)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts DATETIME DEFAULT CURRENT_TIMESTAMP,
			route_user TEXT,
			remote_addr TEXT,
			target_host TEXT,
			target_port INTEGER,
			event_type TEXT,
			detail TEXT,
			exit_status INTEGER,
			truncated INTEGER DEFAULT 0,
			client_key_label TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_logs(ts)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_route_user ON audit_logs(route_user)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("迁移失败 (%s): %w", stmt, err)
		}
	}
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS route_authorized_keys`); err != nil {
		return fmt.Errorf("清理旧表失败: %w", err)
	}
	if err := s.ensureColumn("routes", "listen_password_hash", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("admin_users", "initialized", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("audit_logs", "client_key_label", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("routes", "last_test_at", "DATETIME"); err != nil {
		return err
	}
	if err := s.ensureColumn("routes", "last_test_ok", "INTEGER"); err != nil {
		return err
	}
	return s.ensureColumn("routes", "last_test_error", "TEXT")
}

// ensureColumn 给已存在的旧库补列(SQLite 的 ALTER TABLE 不支持 IF NOT EXISTS)。
func (s *Store) ensureColumn(table, column, decl string) error {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil // 已存在
		}
	}
	rows.Close()

	_, err = s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, decl))
	return err
}

// ---------- settings ----------

func (s *Store) GetSetting(key, def string) string {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return def
	}
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ---------- admin users ----------

type AdminUser struct {
	ID           int64
	Username     string
	PasswordHash string
	Initialized  bool // false 表示还在用初始密码,前端应强制要求先改密码
}

func (s *Store) CountAdminUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&n)
	return n, err
}

// CreateAdminUser 创建管理员账号,initialized 固定为 0(未初始化),
// 首次登录后必须改密码才能进入其他页面。
func (s *Store) CreateAdminUser(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO admin_users(username, password_hash, initialized) VALUES(?, ?, 0)`, username, string(hash))
	return err
}

func (s *Store) GetAdminUser(username string) (*AdminUser, error) {
	var u AdminUser
	var initialized int
	err := s.db.QueryRow(`SELECT id, username, password_hash, initialized FROM admin_users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &initialized)
	if err != nil {
		return nil, err
	}
	u.Initialized = initialized != 0
	return &u, nil
}

// SetAdminPassword 修改密码,同时把 initialized 标记为 1(表示已经完成首次修改密码)。
func (s *Store) SetAdminPassword(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE admin_users SET password_hash = ?, initialized = 1 WHERE username = ?`, string(hash), username)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("管理员用户 %q 不存在", username)
	}
	return nil
}

// ---------- routes ----------

type RouteRecord struct {
	RouteUser                string `json:"route_user"`
	TargetHost               string `json:"target_host"`
	TargetPort               int    `json:"target_port"`
	TargetUser               string `json:"target_user"`
	AuthType                 string `json:"auth_type"` // password | private_key
	AuthPassword             string `json:"auth_password,omitempty"`
	AuthPrivateKey           string `json:"auth_private_key,omitempty"`
	AuthPrivateKeyPassphrase string `json:"auth_private_key_passphrase,omitempty"`

	// 只读,展示当前有哪些客户端密钥关联到了这条路由;密钥本身在"客户端密钥"页面管理。
	ClientKeyLabels []string `json:"client_key_labels"`

	// 客户端(Claude 侧)连 proxy 这一端的备用认证方式:除了关联的客户端公钥,
	// 还可以选配一个密码,两者任一匹配即可登录。
	ListenPassword      string `json:"listen_password,omitempty"`       // 明文,只在设置/修改密码时非空传入
	ClearListenPassword bool   `json:"clear_listen_password,omitempty"` // 传 true 表示移除密码登录,只保留公钥
	HasListenPassword   bool   `json:"has_listen_password"`             // 只读,告知前端当前是否已设置密码

	// 只读,最近一次"测试 SSH 连接"的结果。
	LastTestAt    *time.Time `json:"last_test_at"`
	LastTestOK    *bool      `json:"last_test_ok"`
	LastTestError string     `json:"last_test_error,omitempty"`

	listenPasswordHash string // 内部字段,不参与 JSON 序列化,供认证时比对
}

const routeSelectColumns = `route_user, target_host, target_port, target_user, auth_type,
	auth_password, auth_private_key, auth_private_key_passphrase, listen_password_hash,
	last_test_at, last_test_ok, last_test_error`

func scanRoute(scan func(dest ...any) error) (RouteRecord, error) {
	var r RouteRecord
	var pw, pk, pp, lph, testErr sql.NullString
	var testAt sql.NullTime
	var testOK sql.NullInt64
	if err := scan(&r.RouteUser, &r.TargetHost, &r.TargetPort, &r.TargetUser, &r.AuthType, &pw, &pk, &pp, &lph, &testAt, &testOK, &testErr); err != nil {
		return r, err
	}
	r.AuthPassword, r.AuthPrivateKey, r.AuthPrivateKeyPassphrase = pw.String, pk.String, pp.String
	r.listenPasswordHash = lph.String
	r.HasListenPassword = lph.Valid && lph.String != ""
	if testAt.Valid {
		r.LastTestAt = &testAt.Time
	}
	if testOK.Valid {
		ok := testOK.Int64 != 0
		r.LastTestOK = &ok
	}
	r.LastTestError = testErr.String
	return r, nil
}

func (s *Store) ListRoutes() ([]RouteRecord, error) {
	rows, err := s.db.Query(`SELECT ` + routeSelectColumns + ` FROM routes ORDER BY route_user`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RouteRecord{}
	for rows.Next() {
		r, err := scanRoute(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	rows.Close()

	for i := range out {
		labels, err := s.listClientKeyLabelsForRoute(out[i].RouteUser)
		if err != nil {
			return nil, err
		}
		out[i].ClientKeyLabels = labels
	}
	return out, nil
}

func (s *Store) GetRoute(routeUser string) (*RouteRecord, error) {
	row := s.db.QueryRow(`SELECT `+routeSelectColumns+` FROM routes WHERE route_user = ?`, routeUser)
	r, err := scanRoute(row.Scan)
	if err != nil {
		return nil, err
	}
	labels, err := s.listClientKeyLabelsForRoute(routeUser)
	if err != nil {
		return nil, err
	}
	r.ClientKeyLabels = labels
	return &r, nil
}

func (s *Store) listClientKeyLabelsForRoute(routeUser string) ([]string, error) {
	rows, err := s.db.Query(`SELECT ck.label FROM client_keys ck
		JOIN route_client_keys rck ON rck.client_key_id = ck.id
		WHERE rck.route_user = ? ORDER BY ck.label`, routeUser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := []string{}
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, nil
}

func (s *Store) UpsertRoute(r RouteRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var listenHash sql.NullString
	switch {
	case r.ClearListenPassword:
		// 留空,表示移除密码登录
	case r.ListenPassword != "":
		hash, err := bcrypt.GenerateFromPassword([]byte(r.ListenPassword), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("加密登录密码失败: %w", err)
		}
		listenHash = sql.NullString{String: string(hash), Valid: true}
	default:
		// 前端没传新密码也没要求清除,沿用旧值
		var existing sql.NullString
		if err := tx.QueryRow(`SELECT listen_password_hash FROM routes WHERE route_user = ?`, r.RouteUser).Scan(&existing); err == nil {
			listenHash = existing
		}
	}

	_, err = tx.Exec(`INSERT INTO routes(route_user, target_host, target_port, target_user, auth_type,
			auth_password, auth_private_key, auth_private_key_passphrase, listen_password_hash, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(route_user) DO UPDATE SET
			target_host = excluded.target_host,
			target_port = excluded.target_port,
			target_user = excluded.target_user,
			auth_type = excluded.auth_type,
			auth_password = excluded.auth_password,
			auth_private_key = excluded.auth_private_key,
			auth_private_key_passphrase = excluded.auth_private_key_passphrase,
			listen_password_hash = excluded.listen_password_hash,
			updated_at = CURRENT_TIMESTAMP`,
		r.RouteUser, r.TargetHost, r.TargetPort, r.TargetUser, r.AuthType,
		r.AuthPassword, r.AuthPrivateKey, r.AuthPrivateKeyPassphrase, listenHash)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) DeleteRoute(routeUser string) error {
	_, err := s.db.Exec(`DELETE FROM routes WHERE route_user = ?`, routeUser)
	return err
}

// UpdateRouteTestResult 记录一次"测试 SSH 连接"的结果,供 Web 后台展示。
func (s *Store) UpdateRouteTestResult(routeUser string, ok bool, testErr string) error {
	_, err := s.db.Exec(`UPDATE routes SET last_test_at = CURRENT_TIMESTAMP, last_test_ok = ?, last_test_error = ? WHERE route_user = ?`,
		boolToInt(ok), testErr, routeUser)
	return err
}

// ---------- client keys ----------

// ClientKey 是一个命名的客户端身份(比如某个 Claude Agent 用的一对密钥),
// 通过 RouteUsers 关联到它能登录哪些路由别名,多对多关系。
type ClientKey struct {
	ID         int64    `json:"id"`
	Label      string   `json:"label"`
	PublicKey  string   `json:"public_key"`
	RouteUsers []string `json:"route_users"`
}

func (s *Store) ListClientKeys() ([]ClientKey, error) {
	rows, err := s.db.Query(`SELECT id, label, public_key FROM client_keys ORDER BY label`)
	if err != nil {
		return nil, err
	}
	out := []ClientKey{}
	for rows.Next() {
		var k ClientKey
		if err := rows.Scan(&k.ID, &k.Label, &k.PublicKey); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, k)
	}
	rows.Close()

	for i := range out {
		routeUsers, err := s.listRoutesForClientKey(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].RouteUsers = routeUsers
	}
	return out, nil
}

func (s *Store) GetClientKey(id int64) (*ClientKey, error) {
	var k ClientKey
	err := s.db.QueryRow(`SELECT id, label, public_key FROM client_keys WHERE id = ?`, id).
		Scan(&k.ID, &k.Label, &k.PublicKey)
	if err != nil {
		return nil, err
	}
	routeUsers, err := s.listRoutesForClientKey(id)
	if err != nil {
		return nil, err
	}
	k.RouteUsers = routeUsers
	return &k, nil
}

func (s *Store) listRoutesForClientKey(id int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT route_user FROM route_client_keys WHERE client_key_id = ? ORDER BY route_user`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ru string
		if err := rows.Scan(&ru); err != nil {
			return nil, err
		}
		out = append(out, ru)
	}
	return out, nil
}

// ListClientKeysForRoute 返回关联到某个路由别名的所有客户端密钥,供登录认证时比对使用。
func (s *Store) ListClientKeysForRoute(routeUser string) ([]ClientKey, error) {
	rows, err := s.db.Query(`SELECT ck.id, ck.label, ck.public_key FROM client_keys ck
		JOIN route_client_keys rck ON rck.client_key_id = ck.id
		WHERE rck.route_user = ? ORDER BY ck.label`, routeUser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientKey{}
	for rows.Next() {
		var k ClientKey
		if err := rows.Scan(&k.ID, &k.Label, &k.PublicKey); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

func (s *Store) CreateClientKey(label, publicKey string, routeUsers []string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO client_keys(label, public_key) VALUES(?, ?)`, label, publicKey)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, ru := range routeUsers {
		if _, err := tx.Exec(`INSERT INTO route_client_keys(route_user, client_key_id) VALUES(?, ?)`, ru, id); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (s *Store) UpdateClientKey(id int64, label, publicKey string, routeUsers []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE client_keys SET label = ?, public_key = ? WHERE id = ?`, label, publicKey, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("客户端密钥 %d 不存在", id)
	}

	if _, err := tx.Exec(`DELETE FROM route_client_keys WHERE client_key_id = ?`, id); err != nil {
		return err
	}
	for _, ru := range routeUsers {
		if _, err := tx.Exec(`INSERT INTO route_client_keys(route_user, client_key_id) VALUES(?, ?)`, ru, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteClientKey(id int64) error {
	_, err := s.db.Exec(`DELETE FROM client_keys WHERE id = ?`, id)
	return err
}

// ---------- audit logs ----------

type AuditLog struct {
	ID             int64     `json:"id"`
	Ts             time.Time `json:"ts"`
	RouteUser      string    `json:"route_user"`
	RemoteAddr     string    `json:"remote_addr"`
	TargetHost     string    `json:"target_host"`
	TargetPort     int       `json:"target_port"`
	EventType      string    `json:"event_type"`
	Detail         string    `json:"detail"`
	ExitStatus     *int      `json:"exit_status"`
	Truncated      bool      `json:"truncated"`
	ClientKeyLabel string    `json:"client_key_label"`
}

func (s *Store) InsertAuditLog(a AuditLog) error {
	_, err := s.db.Exec(`INSERT INTO audit_logs(route_user, remote_addr, target_host, target_port, event_type, detail, exit_status, truncated, client_key_label)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.RouteUser, a.RemoteAddr, a.TargetHost, a.TargetPort, a.EventType, a.Detail, a.ExitStatus, boolToInt(a.Truncated), a.ClientKeyLabel)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) ListAuditLogs(limit int, routeUser string) ([]AuditLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query := `SELECT id, ts, route_user, remote_addr, target_host, target_port, event_type, detail, exit_status, truncated, client_key_label
		FROM audit_logs`
	args := []any{}
	if routeUser != "" {
		query += ` WHERE route_user = ?`
		args = append(args, routeUser)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuditLog{}
	for rows.Next() {
		var a AuditLog
		var exitStatus sql.NullInt64
		var truncated int
		var clientKeyLabel sql.NullString
		if err := rows.Scan(&a.ID, &a.Ts, &a.RouteUser, &a.RemoteAddr, &a.TargetHost, &a.TargetPort,
			&a.EventType, &a.Detail, &exitStatus, &truncated, &clientKeyLabel); err != nil {
			return nil, err
		}
		if exitStatus.Valid {
			v := int(exitStatus.Int64)
			a.ExitStatus = &v
		}
		a.Truncated = truncated != 0
		a.ClientKeyLabel = clientKeyLabel.String
		out = append(out, a)
	}
	return out, nil
}

func randomPassword() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
