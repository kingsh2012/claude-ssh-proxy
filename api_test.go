package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninitializedAdminCanOnlyChangePassword(t *testing.T) {
	store := openTestStore(t)
	if err := store.CreateAdminUser("admin", "admin"); err != nil {
		t.Fatal(err)
	}
	proxy := &Proxy{store: store, connections: make(map[uint64]*ActiveConnection)}
	server := httptest.NewServer(NewAPI(store, proxy).Router())
	defer server.Close()

	client := server.Client()
	loginBody := bytes.NewBufferString(`{"Username":"admin","Password":"admin"}`)
	response, err := client.Post(server.URL+"/api/login", "application/json", loginBody)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 {
		t.Fatalf("login failed: status=%d cookies=%v", response.StatusCode, response.Cookies())
	}
	cookie := response.Cookies()[0]

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/servers", nil)
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("uninitialized admin accessed protected API: status=%d", response.StatusCode)
	}

	changeBody := bytes.NewBufferString(`{"OldPassword":"","NewPassword":"new-password"}`)
	request, _ = http.NewRequest(http.MethodPut, server.URL+"/api/admin/password", changeBody)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("password change failed: status=%d", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodGet, server.URL+"/api/servers", nil)
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initialized admin could not access API: status=%d", response.StatusCode)
	}
}

func TestLoginCookieIsSecureBehindHTTPSProxy(t *testing.T) {
	store := openTestStore(t)
	if err := store.CreateAdminUser("admin", "admin"); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, &Proxy{store: store, connections: make(map[uint64]*ActiveConnection)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"Username":"admin","Password":"admin"}`))
	request.Header.Set("X-Forwarded-Proto", "https")
	api.Router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: %d", recorder.Code)
	}
	if cookie := recorder.Result().Cookies()[0]; !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie security attributes are incomplete: %#v", cookie)
	}
}

func TestConnectionsAPI(t *testing.T) {
	store := openTestStore(t)
	if err := store.CreateAdminUser("admin", "new-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminPassword("admin", "new-password"); err != nil {
		t.Fatal(err)
	}
	proxy := &Proxy{store: store, connections: make(map[uint64]*ActiveConnection)}
	proxy.addConnection(ServerRecord{ProxyUser: "alpha", TargetUser: "root", TargetHost: "10.0.0.1", TargetPort: 22}, "192.0.2.1:1234", "agent-a")
	api := NewAPI(store, proxy)
	token, err := api.issueToken("admin")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	api.Router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("connections API failed: %d", recorder.Code)
	}
	var connections []ActiveConnection
	if err := json.NewDecoder(recorder.Body).Decode(&connections); err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0].ClientCredentialLabel != "agent-a" {
		t.Fatalf("unexpected connections: %#v", connections)
	}
}

func TestOpenStoreKeyPathUsesDatabaseDirectory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nested.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store.db.Close()
	if _, err := os.Stat(dbPath + ".key"); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicPortServerValidation(t *testing.T) {
	store := openTestStore(t)
	if err := store.CreateAdminUser("admin", "new-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminPassword("admin", "new-password"); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, &Proxy{store: store, connections: make(map[uint64]*ActiveConnection)})
	token, err := api.issueToken("admin")
	if err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{
		`{"proxy_user":"${PORT}","target_host":"192.168.1.100","route_mode":"dynamic_port","port_min":1,"port_max":65535}`,
		`{"proxy_user":"pcdn-${PORT}","target_host":"192.168.1.100","route_mode":"dynamic_port","port_min":9000,"port_max":8000}`,
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		api.Router().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid dynamic route was accepted: body=%s status=%d", body, recorder.Code)
		}
	}
}
