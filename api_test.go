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

func TestServerMetadataAPI(t *testing.T) {
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
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		api.Router().ServeHTTP(recorder, request)
		return recorder
	}

	if recorder := post(`{"proxy_user":"server-a","target_host":"10.0.0.1","ownership":"network_device","remark":"  核心交换机  "}`); recorder.Code != http.StatusOK {
		t.Fatalf("server metadata was rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	server, err := store.GetServer("server-a")
	if err != nil {
		t.Fatal(err)
	}
	if server.Ownership != "network_device" || server.Remark != "核心交换机" {
		t.Fatalf("server metadata was not normalized: %#v", server)
	}
	if recorder := post(`{"proxy_user":"server-b","target_host":"10.0.0.2","ownership":"unknown"}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid ownership was accepted: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBulkTestServersAPIOnlyTestsSelectedServers(t *testing.T) {
	store := openTestStore(t)
	if err := store.CreateAdminUser("admin", "new-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminPassword("admin", "new-password"); err != nil {
		t.Fatal(err)
	}
	for _, server := range []ServerRecord{
		{ProxyUser: "server-a", TargetHost: "127.0.0.1", TargetPort: 1},
		{ProxyUser: "server-b", TargetHost: "127.0.0.1", TargetPort: 2},
	} {
		if err := store.UpsertServer(server); err != nil {
			t.Fatal(err)
		}
	}
	serverA, _ := store.GetServer("server-a")
	api := NewAPI(store, &Proxy{store: store, agents: NewAgentHub(store), connections: make(map[uint64]*ActiveConnection)})
	token, err := api.issueToken("admin")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"server_ids": []int64{serverA.ID}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/servers/bulk/test", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	api.Router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bulk test failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var tested []ServerRecord
	if err := json.NewDecoder(recorder.Body).Decode(&tested); err != nil {
		t.Fatal(err)
	}
	if len(tested) != 1 || tested[0].ProxyUser != "server-a" || tested[0].LastTestOK == nil || *tested[0].LastTestOK {
		t.Fatalf("unexpected bulk test result: %#v", tested)
	}
	serverB, err := store.GetServer("server-b")
	if err != nil {
		t.Fatal(err)
	}
	if serverB.LastTestAt != nil || serverB.LastTestOK != nil {
		t.Fatalf("unselected server was tested: %#v", serverB)
	}
}

func TestBulkCredentialAPIs(t *testing.T) {
	store := openTestStore(t)
	if err := store.CreateAdminUser("admin", "new-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminPassword("admin", "new-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertServer(ServerRecord{ProxyUser: "server-a", TargetHost: "10.0.0.1", TargetPort: 22}); err != nil {
		t.Fatal(err)
	}
	server, _ := store.GetServer("server-a")
	serverCredentialID, err := store.CreateServerCredential(ServerCredential{
		Label: "server-credential", TargetUser: "root", AuthType: "password", AuthPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	clientCredentialID, err := store.CreateClientCredential(ClientCredential{
		Label: "client-credential", AuthType: "password", Password: "password",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	api := NewAPI(store, &Proxy{store: store, connections: make(map[uint64]*ActiveConnection)})
	token, err := api.issueToken("admin")
	if err != nil {
		t.Fatal(err)
	}
	put := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		api.Router().ServeHTTP(recorder, request)
		return recorder
	}

	serverBody, _ := json.Marshal(map[string]any{
		"server_ids": []int64{server.ID}, "server_credential_id": serverCredentialID, "operation": bulkCredentialReplace,
	})
	if recorder := put("/api/servers/bulk/server-credential", string(serverBody)); recorder.Code != http.StatusOK {
		t.Fatalf("bulk server credential API failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	clientBody, _ := json.Marshal(map[string]any{
		"server_ids": []int64{server.ID}, "client_credential_ids": []int64{clientCredentialID}, "operation": bulkCredentialAdd,
	})
	if recorder := put("/api/servers/bulk/client-credentials", string(clientBody)); recorder.Code != http.StatusOK {
		t.Fatalf("bulk client credential API failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	updated, err := store.GetServer("server-a")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ServerCredentialID == nil || *updated.ServerCredentialID != serverCredentialID {
		t.Fatalf("server credential was not updated: %#v", updated)
	}
	assertClientCredentialIDs(t, store, "server-a", clientCredentialID)
}
