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

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)

	mux.HandleFunc("GET /api/me", a.auth(a.handleMe))
	mux.HandleFunc("PUT /api/admin/password", a.auth(a.handleChangePassword))

	mux.HandleFunc("GET /api/routes", a.auth(a.handleListRoutes))
	mux.HandleFunc("POST /api/routes", a.auth(a.handleUpsertRoute))
	mux.HandleFunc("DELETE /api/routes/{user}", a.auth(a.handleDeleteRoute))
	mux.HandleFunc("POST /api/routes/test-all", a.auth(a.handleTestAllRoutes))
	mux.HandleFunc("POST /api/routes/{user}/test", a.auth(a.handleTestRoute))

	mux.HandleFunc("GET /api/client-keys", a.auth(a.handleListClientKeys))
	mux.HandleFunc("POST /api/client-keys", a.auth(a.handleCreateClientKey))
	mux.HandleFunc("PUT /api/client-keys/{id}", a.auth(a.handleUpdateClientKey))
	mux.HandleFunc("DELETE /api/client-keys/{id}", a.auth(a.handleDeleteClientKey))

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
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.OldPassword)) != nil {
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

func (a *API) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := a.store.ListRoutes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range routes {
		routes[i].AuthPassword = ""
		routes[i].AuthPrivateKey = ""
		routes[i].AuthPrivateKeyPassphrase = ""
	}
	writeJSON(w, routes)
}

func (a *API) handleUpsertRoute(w http.ResponseWriter, r *http.Request) {
	var route RouteRecord
	if !decodeJSON(w, r, &route) {
		return
	}
	if route.RouteUser == "" || route.TargetHost == "" || route.TargetUser == "" {
		writeError(w, http.StatusBadRequest, "route_user / target_host / target_user 不能为空")
		return
	}
	if route.TargetPort == 0 {
		route.TargetPort = 22
	}
	switch route.AuthType {
	case "password":
		if route.AuthPassword == "" {
			if existing, err := a.store.GetRoute(route.RouteUser); err == nil {
				route.AuthPassword = existing.AuthPassword // 前端没传密码(说明没改),沿用旧值
			}
		}
	case "private_key":
		if route.AuthPrivateKey == "" {
			if existing, err := a.store.GetRoute(route.RouteUser); err == nil {
				route.AuthPrivateKey = existing.AuthPrivateKey
				route.AuthPrivateKeyPassphrase = existing.AuthPrivateKeyPassphrase
			}
		}
	default:
		writeError(w, http.StatusBadRequest, "auth_type 必须是 password 或 private_key")
		return
	}

	if err := a.store.UpsertRoute(route); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	user := r.PathValue("user")
	if err := a.store.DeleteRoute(user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// runRouteTest 连一次目标机器,把结果(成功/失败 + 错误信息)写回数据库,返回更新后的路由(不含密码/私钥)。
func (a *API) runRouteTest(routeUser string) (*RouteRecord, error) {
	route, err := a.store.GetRoute(routeUser)
	if err != nil {
		return nil, fmt.Errorf("路由不存在")
	}
	testErr := TestRoute(*route)
	msg := ""
	if testErr != nil {
		msg = testErr.Error()
	}
	if err := a.store.UpdateRouteTestResult(routeUser, testErr == nil, msg); err != nil {
		return nil, err
	}
	updated, err := a.store.GetRoute(routeUser)
	if err != nil {
		return nil, err
	}
	updated.AuthPassword, updated.AuthPrivateKey, updated.AuthPrivateKeyPassphrase = "", "", ""
	return updated, nil
}

func (a *API) handleTestRoute(w http.ResponseWriter, r *http.Request) {
	updated, err := a.runRouteTest(r.PathValue("user"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, updated)
}

func (a *API) handleTestAllRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := a.store.ListRoutes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var wg sync.WaitGroup
	for _, route := range routes {
		wg.Add(1)
		go func(routeUser string) {
			defer wg.Done()
			if _, err := a.runRouteTest(routeUser); err != nil {
				log.Printf("测试路由 %q 失败: %v", routeUser, err)
			}
		}(route.RouteUser)
	}
	wg.Wait()

	updated, err := a.store.ListRoutes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range updated {
		updated[i].AuthPassword, updated[i].AuthPrivateKey, updated[i].AuthPrivateKeyPassphrase = "", "", ""
	}
	writeJSON(w, updated)
}

func (a *API) handleListClientKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.store.ListClientKeys()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, keys)
}

func (a *API) handleCreateClientKey(w http.ResponseWriter, r *http.Request) {
	var body ClientKey
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" || body.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "label / public_key 不能为空")
		return
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(body.PublicKey)); err != nil {
		writeError(w, http.StatusBadRequest, "公钥格式不合法: "+err.Error())
		return
	}
	id, err := a.store.CreateClientKey(body.Label, body.PublicKey, body.RouteUsers)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (a *API) handleUpdateClientKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的 id")
		return
	}
	var body ClientKey
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Label == "" || body.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "label / public_key 不能为空")
		return
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(body.PublicKey)); err != nil {
		writeError(w, http.StatusBadRequest, "公钥格式不合法: "+err.Error())
		return
	}
	if err := a.store.UpdateClientKey(id, body.Label, body.PublicKey, body.RouteUsers); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) handleDeleteClientKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "非法的 id")
		return
	}
	if err := a.store.DeleteClientKey(id); err != nil {
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
	routeUser := r.URL.Query().Get("route_user")
	logs, err := a.store.ListAuditLogs(limit, routeUser)
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
