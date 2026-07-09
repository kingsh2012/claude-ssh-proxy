package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

type API struct {
	store     *Store
	proxy     *Proxy
	jwtSecret []byte
}

func NewAPI(store *Store, proxy *Proxy) *API {
	secret := store.GetSetting("jwt_secret", "")
	if secret == "" {
		secret = randomPassword() + randomPassword()
		_ = store.SetSetting("jwt_secret", secret)
	}
	return &API{store: store, proxy: proxy, jwtSecret: []byte(secret)}
}

func (a *API) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)

	mux.HandleFunc("GET /api/me", a.auth(a.handleMe))
	mux.HandleFunc("PUT /api/admin/password", a.auth(a.handleChangePassword))

	mux.HandleFunc("GET /api/servers", a.auth(a.handleListServers))
	mux.HandleFunc("POST /api/servers", a.auth(a.handleUpsertServer))
	mux.HandleFunc("DELETE /api/servers/{user}", a.auth(a.handleDeleteServer))
	mux.HandleFunc("POST /api/servers/test-all", a.auth(a.handleTestAllServers))
	mux.HandleFunc("POST /api/servers/{user}/test", a.auth(a.handleTestServer))
	mux.HandleFunc("PUT /api/servers/{user}/enabled", a.auth(a.handleSetServerEnabled))

	mux.HandleFunc("GET /api/server-credentials", a.auth(a.handleListServerCredentials))
	mux.HandleFunc("POST /api/server-credentials", a.auth(a.handleCreateServerCredential))
	mux.HandleFunc("PUT /api/server-credentials/{id}", a.auth(a.handleUpdateServerCredential))
	mux.HandleFunc("DELETE /api/server-credentials/{id}", a.auth(a.handleDeleteServerCredential))

	mux.HandleFunc("GET /api/client-credentials", a.auth(a.handleListClientCredentials))
	mux.HandleFunc("POST /api/client-credentials", a.auth(a.handleCreateClientCredential))
	mux.HandleFunc("PUT /api/client-credentials/{id}", a.auth(a.handleUpdateClientCredential))
	mux.HandleFunc("DELETE /api/client-credentials/{id}", a.auth(a.handleDeleteClientCredential))

	mux.HandleFunc("GET /api/settings", a.auth(a.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", a.auth(a.handleUpdateSettings))

	mux.HandleFunc("GET /api/audit", a.auth(a.handleListAudit))

	return mux
}

// ---------- auth middleware ----------

const sessionCookieName = "claude_ssh_proxy_session"

func (a *API) issueToken(username string) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   username,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(a.jwtSecret)
}

func (a *API) verifyToken(tokenStr string) (string, bool) {
	claims := &jwt.RegisteredClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return a.jwtSecret, nil
	})
	if err != nil || !tok.Valid {
		return "", false
	}
	return claims.Subject, true
}

func (a *API) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "未登录")
			return
		}
		username, ok := a.verifyToken(cookie.Value)
		if !ok {
			writeError(w, http.StatusUnauthorized, "登录已过期,请重新登录")
			return
		}
		r = r.WithContext(withUsername(r.Context(), username))
		next(w, r)
	}
}

// ---------- handlers ----------

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if !decodeJSON(w, r, &body) {
		return
	}

	user, err := a.store.GetAdminUser(body.Username)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	token, err := a.issueToken(user.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成登录凭证失败")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   12 * 3600,
	})
	writeJSON(w, map[string]any{"username": user.Username, "initialized": user.Initialized})
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	username := usernameFromContext(r.Context())
	user, err := a.store.GetAdminUser(username)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	writeJSON(w, map[string]any{"username": user.Username, "initialized": user.Initialized})
}

func (a *API) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct{ OldPassword, NewPassword string }
	if !decodeJSON(w, r, &body) {
		return
	}
	username := usernameFromContext(r.Context())
	user, err := a.store.GetAdminUser(username)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	// 还没完成首次登录强制改密码的账号,已经用当前密码登录成功过一次(拿到了 session),
	// 不需要再验证一遍原密码;已初始化过的账号正常修改密码,还是要校验原密码。
	if user.Initialized && bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.OldPassword)) != nil {
		writeError(w, http.StatusUnauthorized, "原密码错误")
		return
	}
	if len(body.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "新密码至少 8 位")
		return
	}
	if err := a.store.SetAdminPassword(username, body.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "修改密码失败")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.store.ListServers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range servers {
		servers[i].AuthPassword = ""
		servers[i].AuthPrivateKey = ""
		servers[i].AuthPrivateKeyPassphrase = ""
	}
	writeJSON(w, servers)
}

func (a *API) handleUpsertServer(w http.ResponseWriter, r *http.Request) {
	var server ServerRecord
	if !decodeJSON(w, r, &server) {
		return
	}
	if server.LoginName == "" || server.TargetHost == "" {
		writeError(w, http.StatusBadRequest, "login_name / target_host 不能为空")
		return
	}
	if server.TargetPort == 0 {
		server.TargetPort = 22
	}

	// 服务器的认证信息完全来自"服务器凭据",这里只要校验(如果指定了)凭据确实存在;
	// 留空表示这条服务器暂时没有可用的认证信息,允许保存,之后再补一个凭据即可。
	if server.ServerCredentialID != nil {
		if _, err := a.store.GetServerCredential(*server.ServerCredentialID); err != nil {
			writeError(w, http.StatusBadRequest, "指定的服务器凭据不存在")
			return
		}
	}

	if err := a.store.UpsertServer(server); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	user := r.PathValue("user")
	if err := a.store.DeleteServer(user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleSetServerEnabled(w http.ResponseWriter, r *http.Request) {
	user := r.PathValue("user")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := a.store.SetServerEnabled(user, body.Enabled); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := a.store.GetServer(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated.AuthPassword, updated.AuthPrivateKey, updated.AuthPrivateKeyPassphrase = "", "", ""
	writeJSON(w, updated)
}

// runServerTest 连一次目标机器,把结果(成功/失败 + 错误信息)写回数据库,返回更新后的服务器(不含密码/私钥)。
func (a *API) runServerTest(loginName string) (*ServerRecord, error) {
	server, err := a.store.GetServer(loginName)
	if err != nil {
		return nil, fmt.Errorf("服务器不存在")
	}
	testErr := TestServer(*server)
	msg := ""
	if testErr != nil {
		msg = testErr.Error()
	}
	if err := a.store.UpdateServerTestResult(loginName, testErr == nil, msg); err != nil {
		return nil, err
	}
	updated, err := a.store.GetServer(loginName)
	if err != nil {
		return nil, err
	}
	updated.AuthPassword, updated.AuthPrivateKey, updated.AuthPrivateKeyPassphrase = "", "", ""
	return updated, nil
}

func (a *API) handleTestServer(w http.ResponseWriter, r *http.Request) {
	updated, err := a.runServerTest(r.PathValue("user"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, updated)
}

func (a *API) handleTestAllServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.store.ListServers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var wg sync.WaitGroup
	for _, server := range servers {
		wg.Add(1)
		go func(loginName string) {
			defer wg.Done()
			if _, err := a.runServerTest(loginName); err != nil {
				log.Printf("测试服务器 %q 失败: %v", loginName, err)
			}
		}(server.LoginName)
	}
	wg.Wait()

	updated, err := a.store.ListServers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range updated {
		updated[i].AuthPassword, updated[i].AuthPrivateKey, updated[i].AuthPrivateKeyPassphrase = "", "", ""
	}
	writeJSON(w, updated)
}

func (a *API) handleListServerCredentials(w http.ResponseWriter, r *http.Request) {
	creds, err := a.store.ListServerCredentials()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range creds {
		creds[i].AuthPassword = ""
		creds[i].AuthPrivateKey = ""
		creds[i].AuthPrivateKeyPassphrase = ""
	}
	writeJSON(w, creds)
}

func validateServerCredentialAuth(c *ServerCredential, existing *ServerCredential) error {
	switch c.AuthType {
	case "password":
		if c.AuthPassword == "" && existing != nil {
			c.AuthPassword = existing.AuthPassword
		}
	case "private_key":
		if c.AuthPrivateKey == "" && existing != nil {
			c.AuthPrivateKey = existing.AuthPrivateKey
			c.AuthPrivateKeyPassphrase = existing.AuthPrivateKeyPassphrase
		}
	default:
		return fmt.Errorf("auth_type 必须是 password 或 private_key")
	}
	return nil
}

func (a *API) handleCreateServerCredential(w http.ResponseWriter, r *http.Request) {
	var body ServerCredential
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" || body.TargetUser == "" {
		writeError(w, http.StatusBadRequest, "label / target_user 不能为空")
		return
	}
	if err := validateServerCredentialAuth(&body, nil); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := a.store.CreateServerCredential(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.store.SetServerCredentialServers(id, body.LoginNames); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (a *API) handleUpdateServerCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的 id")
		return
	}
	var body ServerCredential
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" || body.TargetUser == "" {
		writeError(w, http.StatusBadRequest, "label / target_user 不能为空")
		return
	}
	existing, err := a.store.GetServerCredential(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "服务器凭据不存在")
		return
	}
	if err := validateServerCredentialAuth(&body, existing); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.UpdateServerCredential(id, body); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.store.SetServerCredentialServers(id, body.LoginNames); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleDeleteServerCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的 id")
		return
	}
	if err := a.store.DeleteServerCredential(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleListClientCredentials(w http.ResponseWriter, r *http.Request) {
	creds, err := a.store.ListClientCredentials()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, creds)
}

// validateClientCredentialAuth 校验客户端凭据的 auth_type 和对应字段;公钥类型顺带校验公钥格式。
func validateClientCredentialAuth(c *ClientCredential) error {
	switch c.AuthType {
	case "public_key":
		if c.PublicKey != "" {
			if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.PublicKey)); err != nil {
				return fmt.Errorf("公钥格式不合法: %w", err)
			}
		}
	case "password":
		// 密码留空表示编辑时不修改,Store 层会沿用旧值
	default:
		return fmt.Errorf("auth_type 必须是 public_key 或 password")
	}
	return nil
}

func (a *API) handleCreateClientCredential(w http.ResponseWriter, r *http.Request) {
	var body ClientCredential
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" {
		writeError(w, http.StatusBadRequest, "label 不能为空")
		return
	}
	if err := validateClientCredentialAuth(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := a.store.CreateClientCredential(body, body.LoginNames)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (a *API) handleUpdateClientCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的 id")
		return
	}
	var body ClientCredential
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" {
		writeError(w, http.StatusBadRequest, "label 不能为空")
		return
	}
	if err := validateClientCredentialAuth(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.UpdateClientCredential(id, body, body.LoginNames); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleDeleteClientCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的 id")
		return
	}
	if err := a.store.DeleteClientCredential(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"listen_addr": a.store.GetSetting("listen_addr", ":2222"),
	})
}

func (a *API) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ListenAddr string `json:"listen_addr"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ListenAddr == "" {
		writeError(w, http.StatusBadRequest, "listen_addr 不能为空")
		return
	}
	if err := a.proxy.Restart(body.ListenAddr); err != nil {
		writeError(w, http.StatusBadRequest, "监听地址无效: "+err.Error())
		return
	}
	_ = a.store.SetSetting("listen_addr", body.ListenAddr)
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleListAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	loginName := r.URL.Query().Get("login_name")
	logs, err := a.store.ListAuditLogs(limit, loginName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, logs)
}

// ---------- helpers ----------

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("写响应失败: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
