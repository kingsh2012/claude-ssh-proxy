package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
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
		`CREATE TABLE IF NOT EXISTS servers (
			login_name TEXT PRIMARY KEY,
			target_host TEXT NOT NULL,
			target_port INTEGER NOT NULL DEFAULT 22,
			enabled INTEGER NOT NULL DEFAULT 1,
			server_credential_id INTEGER,
			last_test_at DATETIME,
			last_test_ok INTEGER,
			last_test_error TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS server_credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			label TEXT NOT NULL,
			target_user TEXT NOT NULL,
			auth_type TEXT NOT NULL,
			auth_password TEXT,
			auth_private_key TEXT,
			auth_private_key_passphrase TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS client_credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			label TEXT NOT NULL,
			auth_type TEXT NOT NULL,
			public_key TEXT,
			password_hash TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS server_client_credentials (
			login_name TEXT NOT NULL REFERENCES servers(login_name) ON DELETE CASCADE,
			client_credential_id INTEGER NOT NULL REFERENCES client_credentials(id) ON DELETE CASCADE,
			PRIMARY KEY (login_name, client_credential_id)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts DATETIME DEFAULT CURRENT_TIMESTAMP,
			login_name TEXT,
			remote_addr TEXT,
			target_host TEXT,
			target_port INTEGER,
			event_type TEXT,
			detail TEXT,
			exit_status INTEGER,
			truncated INTEGER DEFAULT 0,
			client_credential_label TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_logs(ts)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_login_name ON audit_logs(login_name)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("迁移失败 (%s): %w", stmt, err)
		}
	}
	return nil
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

// ---------- servers ----------

type ServerRecord struct {
	LoginName  string `json:"login_name"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`

	// 下面这些认证相关字段都是只读的,完全来自关联的"服务器凭据"(见 ServerCredentialID),
	// 不能通过 UpsertServer 直接设置;服务器本身不再存密码/私钥/目标用户名。
	TargetUser               string `json:"target_user,omitempty"`
	AuthType                 string `json:"auth_type,omitempty"`
	AuthPassword             string `json:"auth_password,omitempty"`
	AuthPrivateKey           string `json:"auth_private_key,omitempty"`
	AuthPrivateKeyPassphrase string `json:"auth_private_key_passphrase,omitempty"`

	// 是否允许连接;禁用后,不管客户端凭据/共享凭据对不对,一律拒绝这个别名的登录。
	Enabled bool `json:"enabled"`

	// 只读,展示当前有哪些客户端凭据关联到了这条服务器;凭据本身在"客户端凭据"页面管理。
	ClientCredentialLabels []string `json:"client_credential_labels"`

	// 只读,最近一次"测试 SSH 连接"的结果。
	LastTestAt    *time.Time `json:"last_test_at"`
	LastTestOK    *bool      `json:"last_test_ok"`
	LastTestError string     `json:"last_test_error,omitempty"`

	// 连目标机器必须指定一个"服务器凭据"(server_credentials 表,包含目标用户名+密码/私钥),
	// 多台服务器可以共用同一份、改一处全部生效。留空表示这条服务器暂时没有可用的认证信息。
	ServerCredentialID    *int64 `json:"server_credential_id"`
	ServerCredentialLabel string `json:"server_credential_label,omitempty"` // 只读
}

const serverSelectColumns = `login_name, target_host, target_port, enabled,
	last_test_at, last_test_ok, last_test_error, server_credential_id`

func scanServer(scan func(dest ...any) error) (ServerRecord, error) {
	var r ServerRecord
	var testErr sql.NullString
	var enabled int
	var testAt sql.NullTime
	var testOK sql.NullInt64
	var credID sql.NullInt64
	if err := scan(&r.LoginName, &r.TargetHost, &r.TargetPort, &enabled, &testAt, &testOK, &testErr, &credID); err != nil {
		return r, err
	}
	r.Enabled = enabled != 0
	if testAt.Valid {
		r.LastTestAt = &testAt.Time
	}
	if testOK.Valid {
		ok := testOK.Int64 != 0
		r.LastTestOK = &ok
	}
	r.LastTestError = testErr.String
	if credID.Valid {
		r.ServerCredentialID = &credID.Int64
	}
	return r, nil
}

// resolveServerCredential 把这条服务器关联的"服务器凭据"里的目标用户名/密码/私钥读出来,
// 填进 ServerRecord 的只读字段,供拨号连接和 Web API 展示使用。没关联凭据时这些字段留空,
// 服务器处于"暂不可连接"的状态,需要去编辑指定一个凭据。
func (s *Store) resolveServerCredential(r *ServerRecord) error {
	if r.ServerCredentialID == nil {
		return nil
	}
	var label, targetUser, authType string
	var pw, pk, pp sql.NullString
	err := s.db.QueryRow(`SELECT label, target_user, auth_type, auth_password, auth_private_key, auth_private_key_passphrase
		FROM server_credentials WHERE id = ?`, *r.ServerCredentialID).
		Scan(&label, &targetUser, &authType, &pw, &pk, &pp)
	if err != nil {
		return fmt.Errorf("共享凭据 %d 不存在: %w", *r.ServerCredentialID, err)
	}
	r.ServerCredentialLabel = label
	r.TargetUser = targetUser
	r.AuthType = authType
	r.AuthPassword = pw.String
	r.AuthPrivateKey = pk.String
	r.AuthPrivateKeyPassphrase = pp.String
	return nil
}

func (s *Store) ListServers() ([]ServerRecord, error) {
	rows, err := s.db.Query(`SELECT ` + serverSelectColumns + ` FROM servers ORDER BY login_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ServerRecord{}
	for rows.Next() {
		r, err := scanServer(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	rows.Close()

	for i := range out {
		labels, err := s.listClientCredentialLabelsForServer(out[i].LoginName)
		if err != nil {
			return nil, err
		}
		out[i].ClientCredentialLabels = labels
		if err := s.resolveServerCredential(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) GetServer(loginName string) (*ServerRecord, error) {
	row := s.db.QueryRow(`SELECT `+serverSelectColumns+` FROM servers WHERE login_name = ?`, loginName)
	r, err := scanServer(row.Scan)
	if err != nil {
		return nil, err
	}
	labels, err := s.listClientCredentialLabelsForServer(loginName)
	if err != nil {
		return nil, err
	}
	r.ClientCredentialLabels = labels
	if err := s.resolveServerCredential(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) listClientCredentialLabelsForServer(loginName string) ([]string, error) {
	rows, err := s.db.Query(`SELECT cc.label FROM client_credentials cc
		JOIN server_client_credentials rcc ON rcc.client_credential_id = cc.id
		WHERE rcc.login_name = ? ORDER BY cc.label`, loginName)
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

func (s *Store) UpsertServer(r ServerRecord) error {
	var credentialID sql.NullInt64
	if r.ServerCredentialID != nil {
		credentialID = sql.NullInt64{Int64: *r.ServerCredentialID, Valid: true}
	}

	// enabled 不在这里改:新建时用表的 DEFAULT 1,编辑已有服务器时保留原值,
	// 是否启用由 SetServerEnabled 单独控制,避免保存其他字段时不小心把开关状态带跑偏。
	_, err := s.db.Exec(`INSERT INTO servers(login_name, target_host, target_port, server_credential_id, updated_at)
		VALUES(?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(login_name) DO UPDATE SET
			target_host = excluded.target_host,
			target_port = excluded.target_port,
			server_credential_id = excluded.server_credential_id,
			updated_at = CURRENT_TIMESTAMP`,
		r.LoginName, r.TargetHost, r.TargetPort, credentialID)
	return err
}

// SetServerEnabled 启用/禁用一条服务器;禁用后 proxy 会在认证阶段直接拒绝这个别名的登录,
// 不管客户端凭据或共享凭据是否匹配。
func (s *Store) SetServerEnabled(loginName string, enabled bool) error {
	res, err := s.db.Exec(`UPDATE servers SET enabled = ? WHERE login_name = ?`, boolToInt(enabled), loginName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("服务器 %q 不存在", loginName)
	}
	return nil
}

func (s *Store) DeleteServer(loginName string) error {
	_, err := s.db.Exec(`DELETE FROM servers WHERE login_name = ?`, loginName)
	return err
}

// UpdateServerTestResult 记录一次"测试 SSH 连接"的结果,供 Web 后台展示。
func (s *Store) UpdateServerTestResult(loginName string, ok bool, testErr string) error {
	_, err := s.db.Exec(`UPDATE servers SET last_test_at = CURRENT_TIMESTAMP, last_test_ok = ?, last_test_error = ? WHERE login_name = ?`,
		boolToInt(ok), testErr, loginName)
	return err
}

// ---------- 服务器凭据(server_credentials) ----------

// ServerCredential 是一份命名的、可以被多台服务器共用的后端认证信息(密码或私钥)。
// 很多服务器用同一套密码/私钥登录时,不用在每台服务器里各存一份,改一处、所有引用它的
// 服务器都跟着生效。LoginNames 是只读字段,展示当前有哪些服务器在用这份凭据。
type ServerCredential struct {
	ID                       int64    `json:"id"`
	Label                    string   `json:"label"`
	TargetUser               string   `json:"target_user"`
	AuthType                 string   `json:"auth_type"` // password | private_key
	AuthPassword             string   `json:"auth_password,omitempty"`
	AuthPrivateKey           string   `json:"auth_private_key,omitempty"`
	AuthPrivateKeyPassphrase string   `json:"auth_private_key_passphrase,omitempty"`
	LoginNames               []string `json:"login_names"`
}

func (s *Store) ListServerCredentials() ([]ServerCredential, error) {
	rows, err := s.db.Query(`SELECT id, label, target_user, auth_type, auth_password, auth_private_key, auth_private_key_passphrase
		FROM server_credentials ORDER BY label`)
	if err != nil {
		return nil, err
	}
	out := []ServerCredential{}
	for rows.Next() {
		var c ServerCredential
		var pw, pk, pp sql.NullString
		if err := rows.Scan(&c.ID, &c.Label, &c.TargetUser, &c.AuthType, &pw, &pk, &pp); err != nil {
			rows.Close()
			return nil, err
		}
		c.AuthPassword, c.AuthPrivateKey, c.AuthPrivateKeyPassphrase = pw.String, pk.String, pp.String
		out = append(out, c)
	}
	rows.Close()

	for i := range out {
		loginNames, err := s.listServersUsingServerCredential(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].LoginNames = loginNames
	}
	return out, nil
}

func (s *Store) GetServerCredential(id int64) (*ServerCredential, error) {
	var c ServerCredential
	var pw, pk, pp sql.NullString
	err := s.db.QueryRow(`SELECT id, label, target_user, auth_type, auth_password, auth_private_key, auth_private_key_passphrase
		FROM server_credentials WHERE id = ?`, id).
		Scan(&c.ID, &c.Label, &c.TargetUser, &c.AuthType, &pw, &pk, &pp)
	if err != nil {
		return nil, err
	}
	c.AuthPassword, c.AuthPrivateKey, c.AuthPrivateKeyPassphrase = pw.String, pk.String, pp.String
	loginNames, err := s.listServersUsingServerCredential(id)
	if err != nil {
		return nil, err
	}
	c.LoginNames = loginNames
	return &c, nil
}

func (s *Store) listServersUsingServerCredential(id int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT login_name FROM servers WHERE server_credential_id = ? ORDER BY login_name`, id)
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

func (s *Store) CreateServerCredential(c ServerCredential) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO server_credentials(label, target_user, auth_type, auth_password, auth_private_key, auth_private_key_passphrase)
		VALUES(?, ?, ?, ?, ?, ?)`,
		c.Label, c.TargetUser, c.AuthType, c.AuthPassword, c.AuthPrivateKey, c.AuthPrivateKeyPassphrase)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateServerCredential(id int64, c ServerCredential) error {
	res, err := s.db.Exec(`UPDATE server_credentials SET label = ?, target_user = ?, auth_type = ?, auth_password = ?,
		auth_private_key = ?, auth_private_key_passphrase = ? WHERE id = ?`,
		c.Label, c.TargetUser, c.AuthType, c.AuthPassword, c.AuthPrivateKey, c.AuthPrivateKeyPassphrase, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("服务器凭据 %d 不存在", id)
	}
	return nil
}

// DeleteServerCredential 删除前检查有没有服务器还在用这份凭据,有的话拒绝删除,
// 避免这些服务器突然失去认证信息、连不上。
func (s *Store) DeleteServerCredential(id int64) error {
	loginNames, err := s.listServersUsingServerCredential(id)
	if err != nil {
		return err
	}
	if len(loginNames) > 0 {
		return fmt.Errorf("还有 %d 台服务器在使用这份凭据(%s),请先改成其他凭据或单独指定认证方式,再删除",
			len(loginNames), strings.Join(loginNames, ", "))
	}
	_, err = s.db.Exec(`DELETE FROM server_credentials WHERE id = ?`, id)
	return err
}

// SetServerCredentialServers 让"哪些服务器使用这份凭据"变成刚好是 loginNames 这个列表:
// 不在列表里但之前用着的服务器会被解除关联(server_credential_id 置空),这些服务器的
// auth_password/auth_private_key 之前用共享凭据时就是空的,解除后需要单独重新设置认证方式。
func (s *Store) SetServerCredentialServers(credID int64, loginNames []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE servers SET server_credential_id = NULL WHERE server_credential_id = ?`, credID); err != nil {
		return err
	}
	for _, ru := range loginNames {
		res, err := tx.Exec(`UPDATE servers SET server_credential_id = ? WHERE login_name = ?`, credID, ru)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("服务器 %q 不存在", ru)
		}
	}
	return tx.Commit()
}

// ---------- 客户端凭据(client_credentials) ----------

// ClientCredential 是一个命名的客户端身份(比如某个 Claude Agent),认证方式是公钥或密码,
// 通过 LoginNames 关联到它能登录哪些服务器,多对多关系:一份凭据可以关联多台服务器,
// 一台服务器也可以被多份凭据共用,任一凭据匹配即可登录。
type ClientCredential struct {
	ID          int64    `json:"id"`
	Label       string   `json:"label"`
	AuthType    string   `json:"auth_type"` // public_key | password
	PublicKey   string   `json:"public_key,omitempty"`
	Password    string   `json:"password,omitempty"` // 明文,只在设置/修改密码时非空传入
	HasPassword bool     `json:"has_password"`       // 只读,告知前端当前是否已设置密码
	LoginNames  []string `json:"login_names"`

	passwordHash string // 内部字段,不参与 JSON 序列化,供认证时比对
}

func scanClientCredential(scan func(dest ...any) error) (ClientCredential, error) {
	var c ClientCredential
	var pubKey, pwHash sql.NullString
	if err := scan(&c.ID, &c.Label, &c.AuthType, &pubKey, &pwHash); err != nil {
		return c, err
	}
	c.PublicKey = pubKey.String
	c.passwordHash = pwHash.String
	c.HasPassword = pwHash.Valid && pwHash.String != ""
	return c, nil
}

func (s *Store) ListClientCredentials() ([]ClientCredential, error) {
	rows, err := s.db.Query(`SELECT id, label, auth_type, public_key, password_hash FROM client_credentials ORDER BY label`)
	if err != nil {
		return nil, err
	}
	out := []ClientCredential{}
	for rows.Next() {
		c, err := scanClientCredential(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()

	for i := range out {
		loginNames, err := s.listServersForClientCredential(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].LoginNames = loginNames
	}
	return out, nil
}

func (s *Store) GetClientCredential(id int64) (*ClientCredential, error) {
	row := s.db.QueryRow(`SELECT id, label, auth_type, public_key, password_hash FROM client_credentials WHERE id = ?`, id)
	c, err := scanClientCredential(row.Scan)
	if err != nil {
		return nil, err
	}
	loginNames, err := s.listServersForClientCredential(id)
	if err != nil {
		return nil, err
	}
	c.LoginNames = loginNames
	return &c, nil
}

func (s *Store) listServersForClientCredential(id int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT login_name FROM server_client_credentials WHERE client_credential_id = ? ORDER BY login_name`, id)
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

// ListClientCredentialsForServer 返回关联到某个服务器的所有客户端凭据,供登录认证时比对使用
// (公钥类型比对 PublicKey,密码类型比对内部的 passwordHash)。
func (s *Store) ListClientCredentialsForServer(loginName string) ([]ClientCredential, error) {
	rows, err := s.db.Query(`SELECT cc.id, cc.label, cc.auth_type, cc.public_key, cc.password_hash FROM client_credentials cc
		JOIN server_client_credentials rcc ON rcc.client_credential_id = cc.id
		WHERE rcc.login_name = ? ORDER BY cc.label`, loginName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientCredential{}
	for rows.Next() {
		c, err := scanClientCredential(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) CreateClientCredential(c ClientCredential, loginNames []string) (int64, error) {
	pubKey, pwHash, err := clientCredentialAuthColumns(c, nil)
	if err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO client_credentials(label, auth_type, public_key, password_hash) VALUES(?, ?, ?, ?)`,
		c.Label, c.AuthType, pubKey, pwHash)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, ru := range loginNames {
		if _, err := tx.Exec(`INSERT INTO server_client_credentials(login_name, client_credential_id) VALUES(?, ?)`, ru, id); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (s *Store) UpdateClientCredential(id int64, c ClientCredential, loginNames []string) error {
	existing, err := s.GetClientCredential(id)
	if err != nil {
		return fmt.Errorf("客户端凭据 %d 不存在", id)
	}
	pubKey, pwHash, err := clientCredentialAuthColumns(c, existing)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE client_credentials SET label = ?, auth_type = ?, public_key = ?, password_hash = ? WHERE id = ?`,
		c.Label, c.AuthType, pubKey, pwHash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("客户端凭据 %d 不存在", id)
	}

	if _, err := tx.Exec(`DELETE FROM server_client_credentials WHERE client_credential_id = ?`, id); err != nil {
		return err
	}
	for _, ru := range loginNames {
		if _, err := tx.Exec(`INSERT INTO server_client_credentials(login_name, client_credential_id) VALUES(?, ?)`, ru, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// clientCredentialAuthColumns 根据 auth_type 计算要写进 public_key / password_hash 两列的值:
// 公钥类型直接存明文公钥;密码类型对明文密码做 bcrypt 哈希,如果这次没传新密码就沿用 existing 的哈希。
func clientCredentialAuthColumns(c ClientCredential, existing *ClientCredential) (sql.NullString, sql.NullString, error) {
	var pubKey, pwHash sql.NullString
	switch c.AuthType {
	case "public_key":
		if c.PublicKey != "" {
			pubKey = sql.NullString{String: c.PublicKey, Valid: true}
		} else if existing != nil {
			pubKey = sql.NullString{String: existing.PublicKey, Valid: existing.PublicKey != ""}
		}
	case "password":
		if c.Password != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
			if err != nil {
				return pubKey, pwHash, fmt.Errorf("加密密码失败: %w", err)
			}
			pwHash = sql.NullString{String: string(hash), Valid: true}
		} else if existing != nil {
			pwHash = sql.NullString{String: existing.passwordHash, Valid: existing.passwordHash != ""}
		}
	default:
		return pubKey, pwHash, fmt.Errorf("auth_type 必须是 public_key 或 password")
	}
	return pubKey, pwHash, nil
}

func (s *Store) DeleteClientCredential(id int64) error {
	_, err := s.db.Exec(`DELETE FROM client_credentials WHERE id = ?`, id)
	return err
}

// ---------- audit logs ----------

type AuditLog struct {
	ID                    int64     `json:"id"`
	Ts                    time.Time `json:"ts"`
	LoginName             string    `json:"login_name"`
	RemoteAddr            string    `json:"remote_addr"`
	TargetHost            string    `json:"target_host"`
	TargetPort            int       `json:"target_port"`
	EventType             string    `json:"event_type"`
	Detail                string    `json:"detail"`
	ExitStatus            *int      `json:"exit_status"`
	Truncated             bool      `json:"truncated"`
	ClientCredentialLabel string    `json:"client_credential_label"`
}

func (s *Store) InsertAuditLog(a AuditLog) error {
	_, err := s.db.Exec(`INSERT INTO audit_logs(login_name, remote_addr, target_host, target_port, event_type, detail, exit_status, truncated, client_credential_label)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.LoginName, a.RemoteAddr, a.TargetHost, a.TargetPort, a.EventType, a.Detail, a.ExitStatus, boolToInt(a.Truncated), a.ClientCredentialLabel)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) ListAuditLogs(limit int, loginName string) ([]AuditLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query := `SELECT id, ts, login_name, remote_addr, target_host, target_port, event_type, detail, exit_status, truncated, client_credential_label
		FROM audit_logs`
	args := []any{}
	if loginName != "" {
		query += ` WHERE login_name = ?`
		args = append(args, loginName)
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
		var clientCredentialLabel sql.NullString
		if err := rows.Scan(&a.ID, &a.Ts, &a.LoginName, &a.RemoteAddr, &a.TargetHost, &a.TargetPort,
			&a.EventType, &a.Detail, &exitStatus, &truncated, &clientCredentialLabel); err != nil {
			return nil, err
		}
		if exitStatus.Valid {
			v := int(exitStatus.Int64)
			a.ExitStatus = &v
		}
		a.Truncated = truncated != 0
		a.ClientCredentialLabel = clientCredentialLabel.String
		out = append(out, a)
	}
	return out, nil
}

func randomPassword() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
