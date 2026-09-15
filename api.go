package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
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

	mux.HandleFunc("GET /api/agent-enrollments", a.auth(a.handleListEnrollments))
	mux.HandleFunc("POST /api/agent-enrollments", a.auth(a.handleCreateEnrollment))
	mux.HandleFunc("POST /api/agent-enrollments/{id}/revoke", a.auth(a.handleRevokeEnrollment))
	mux.HandleFunc("GET /api/servers", a.auth(a.handleListServers))
	mux.HandleFunc("POST /api/servers", a.auth(a.handleUpsertServer))
	mux.HandleFunc("DELETE /api/servers/{user}", a.auth(a.handleDeleteServer))
	mux.HandleFunc("POST /api/servers/test-all", a.auth(a.handleTestAllServers))
	mux.HandleFunc("POST /api/servers/{user}/agent-token", a.auth(a.handleRotateAgentToken))
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

	mux.HandleFunc("PUT /api/settings/agent-registration/address", a.auth(a.handleSaveRegistrationAddress))
	mux.HandleFunc("POST /api/settings/agent-registration/generate", a.auth(a.handleGenerateSelfRegistration))
	mux.HandleFunc("GET /api/settings/agent-registration", a.auth(a.handleGetSelfRegistration))
	mux.HandleFunc("PUT /api/settings/agent-registration", a.auth(a.handlePutSelfRegistration))
	mux.HandleFunc("DELETE /api/settings/agent-registration", a.auth(a.handleDisableSelfRegistration))
	mux.HandleFunc("GET /api/settings/listeners", a.auth(a.handleGetListeners))
	mux.HandleFunc("PUT /api/settings/listeners", a.auth(a.handlePutListeners))
	mux.HandleFunc("GET /api/settings", a.auth(a.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", a.auth(a.handleUpdateSettings))

	mux.HandleFunc("GET /api/audit", a.auth(a.handleListAudit))
	mux.HandleFunc("GET /api/connections", a.auth(a.handleListConnections))

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
		user, err := a.store.GetAdminUser(username)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "用户不存在,请重新登录")
			return
		}
		if !user.Initialized && r.URL.Path != "/api/me" && r.URL.Path != "/api/admin/password" && r.URL.Path != "/api/logout" {
			writeError(w, http.StatusForbidden, "首次登录必须先修改密码")
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
		Secure:   requestUsesHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   12 * 3600,
	})
	writeJSON(w, map[string]any{"username": user.Username, "initialized": user.Initialized})
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: requestUsesHTTPS(r), SameSite: http.SameSiteStrictMode})
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
		servers[i].AgentOnline = a.proxy.agents.Online(servers[i].ID)
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
	if server.ConnectionType == "" {
		server.ConnectionType = "ssh"
	}
	if server.ConnectionType != "ssh" && server.ConnectionType != "agent" {
		writeError(w, 400, "未知接入类型")
		return
	}
	if server.ConnectionType == "agent" {
		if server.RouteMode != "" && server.RouteMode != "fixed" {
			writeError(w, 400, "Agent仅支持固定登录名")
			return
		}
		server.TargetHost = "Windows Agent"
		server.ServerCredentialID = nil
		server.LegacyAlgorithms = false
		server.HostKeyFingerprint = ""
	}
	if server.ProxyUser == "" || server.TargetHost == "" {
		writeError(w, http.StatusBadRequest, "proxy_user / target_host不能为空")
		return
	}
	switch server.RouteMode {
	case "", "fixed":
		server.RouteMode = "fixed"
		if strings.Contains(server.ProxyUser, "${PORT}") {
			writeError(w, http.StatusBadRequest, "固定端口模式的代理登录名不能包含 ${PORT}")
			return
		}
		if server.TargetPort == 0 {
			server.TargetPort = 22
		}
		if server.TargetPort < 1 || server.TargetPort > 65535 {
			writeError(w, http.StatusBadRequest, "target_port必须在 1-65535 之间")
			return
		}
		server.PortMin, server.PortMax = 1, 65535
	case "dynamic_port":
		if strings.Count(server.ProxyUser, "${PORT}") != 1 || !strings.HasSuffix(server.ProxyUser, "${PORT}") || server.ProxyUser == "${PORT}" {
			writeError(w, http.StatusBadRequest, "动态端口模式的代理登录名必须是非空前缀加 ${PORT},例如server-${PORT}")
			return
		}
		if server.PortMin < 1 || server.PortMax > 65535 || server.PortMin > server.PortMax {
			writeError(w, http.StatusBadRequest, "允许端口范围必须在 1-65535 之间,且起始端口不能大于结束端口")
			return
		}
		server.TargetPort = 0
	default:
		writeError(w, http.StatusBadRequest, "route_mode必须是fixed或dynamic_port")
		return
	}
	server.HostKeyFingerprint = strings.TrimSpace(server.HostKeyFingerprint)
	if server.HostKeyFingerprint != "" && !validSHA256Fingerprint(server.HostKeyFingerprint) {
		writeError(w, http.StatusBadRequest, "host_key_fingerprint必须是合法的SHA256 SSH指纹")
		return
	}

	// 服务器的认证信息完全来自"服务器凭证",这里只要校验(如果指定了)凭证确实存在;
	// 留空表示这条服务器暂时没有可用的认证信息,允许保存,之后再补一个凭证即可。
	if server.ServerCredentialID != nil {
		if _, err := a.store.GetServerCredential(*server.ServerCredentialID); err != nil {
			writeError(w, http.StatusBadRequest, "指定的服务器凭证不存在")
			return
		}
	}

	if err := a.store.UpsertServer(server); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.proxy.agents.Disconnect(server.ID)
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	user := r.PathValue("user")
	previous, _ := a.store.GetServer(user)
	if err := a.store.DeleteServer(user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if previous != nil {
		a.proxy.agents.Disconnect(previous.ID)
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
	if !updated.Enabled {
		a.proxy.agents.Disconnect(updated.ID)
	}
	updated.AgentOnline = a.proxy.agents.Online(updated.ID)
	updated.AuthPassword, updated.AuthPrivateKey, updated.AuthPrivateKeyPassphrase = "", "", ""
	writeJSON(w, updated)
}

// runServerTest 连一次目标机器,把结果(成功/失败 + 错误信息)写回数据库,返回更新后的服务器(不含密码/私钥)。
func (a *API) runServerTest(proxyUser string) (*ServerRecord, error) {
	server, err := a.store.GetServer(proxyUser)
	if err != nil {
		return nil, fmt.Errorf("服务器不存在")
	}
	if server.RouteMode == "dynamic_port" {
		return nil, fmt.Errorf("动态端口规则没有固定目标端口,请使用实际代理登录名连接测试")
	}
	var testErr error
	if server.ConnectionType == "agent" {
		if !server.Enabled || !a.proxy.agents.Online(server.ID) {
			testErr = fmt.Errorf("Agent未连接或已禁用")
		}
	} else {
		testErr = TestServer(*server)
	}
	msg := ""
	if testErr != nil {
		msg = testErr.Error()
	}
	if err := a.store.UpdateServerTestResult(proxyUser, testErr == nil, msg); err != nil {
		return nil, err
	}
	updated, err := a.store.GetServer(proxyUser)
	if err != nil {
		return nil, err
	}
	if !updated.Enabled {
		a.proxy.agents.Disconnect(updated.ID)
	}
	updated.AgentOnline = a.proxy.agents.Online(updated.ID)
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
		if server.RouteMode == "dynamic_port" {
			continue
		}
		wg.Add(1)
		go func(proxyUser string) {
			defer wg.Done()
			if _, err := a.runServerTest(proxyUser); err != nil {
				log.Printf("测试服务器 %q失败: %v", proxyUser, err)
			}
		}(server.ProxyUser)
	}
	wg.Wait()

	updated, err := a.store.ListServers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range updated {
		updated[i].AgentOnline = a.proxy.agents.Online(updated[i].ID)
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
		return fmt.Errorf("auth_type必须是password或private_key")
	}
	return nil
}

func (a *API) handleCreateServerCredential(w http.ResponseWriter, r *http.Request) {
	var body ServerCredential
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" || body.TargetUser == "" {
		writeError(w, http.StatusBadRequest, "label / target_user不能为空")
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
	if err := a.store.SetServerCredentialServers(id, body.ProxyUsers); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (a *API) handleUpdateServerCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的id")
		return
	}
	var body ServerCredential
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" || body.TargetUser == "" {
		writeError(w, http.StatusBadRequest, "label / target_user不能为空")
		return
	}
	existing, err := a.store.GetServerCredential(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "服务器凭证不存在")
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
	if err := a.store.SetServerCredentialServers(id, body.ProxyUsers); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleDeleteServerCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的id")
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

// validateClientCredentialAuth 校验客户端凭证的 auth_type 和对应字段;公钥类型顺带校验公钥格式。
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
		return fmt.Errorf("auth_type必须是public_key或password")
	}
	return nil
}

func (a *API) handleCreateClientCredential(w http.ResponseWriter, r *http.Request) {
	var body ClientCredential
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" {
		writeError(w, http.StatusBadRequest, "label不能为空")
		return
	}
	if err := validateClientCredentialAuth(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := a.store.CreateClientCredential(body, body.ProxyUsers)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (a *API) handleUpdateClientCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的id")
		return
	}
	var body ClientCredential
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" {
		writeError(w, http.StatusBadRequest, "label不能为空")
		return
	}
	if err := validateClientCredentialAuth(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.UpdateClientCredential(id, body, body.ProxyUsers); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleDeleteClientCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的id")
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
		writeError(w, http.StatusBadRequest, "listen_addr不能为空")
		return
	}
	oldAddr := a.proxy.ListenAddr()
	if err := a.proxy.Restart(body.ListenAddr); err != nil {
		writeError(w, http.StatusBadRequest, "监听地址无效: "+err.Error())
		return
	}
	if err := a.store.SetSetting("listen_addr", body.ListenAddr); err != nil {
		rollbackErr := a.proxy.Restart(oldAddr)
		if rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("保存监听地址失败,且恢复旧监听失败: %v", rollbackErr))
			return
		}
		writeError(w, http.StatusInternalServerError, "保存监听地址失败,已恢复旧监听")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleListAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	filters := AuditFilters{
		ProxyUser:             r.URL.Query().Get("proxy_user"),
		TargetHost:            r.URL.Query().Get("target_host"),
		ClientCredentialLabel: r.URL.Query().Get("client_credential_label"),
	}
	logs, err := a.store.ListAuditLogs(limit, filters)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, logs)
}

func (a *API) handleListConnections(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.proxy.ActiveConnections())
}

// ---------- helpers ----------

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法JSON")
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

func requestUsesHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func validSHA256Fingerprint(value string) bool {
	encoded := strings.TrimPrefix(value, "SHA256:")
	if encoded == value {
		return false
	}
	digest, err := base64.RawStdEncoding.DecodeString(encoded)
	return err == nil && len(digest) == 32
}
