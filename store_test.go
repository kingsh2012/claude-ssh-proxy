package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.db.Close() })
	return store
}

func TestServerCredentialsAreEncryptedAtRest(t *testing.T) {
	store := openTestStore(t)
	id, err := store.CreateServerCredential(ServerCredential{
		Label: "production", TargetUser: "root", AuthType: "private_key",
		AuthPassword: "server-password", AuthPrivateKey: "private-key", AuthPrivateKeyPassphrase: "key-password",
	})
	if err != nil {
		t.Fatal(err)
	}

	var password, privateKey, passphrase string
	if err := store.db.QueryRow(`SELECT auth_password, auth_private_key, auth_private_key_passphrase FROM server_credentials WHERE id = ?`, id).
		Scan(&password, &privateKey, &passphrase); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"password": password, "private key": privateKey, "passphrase": passphrase} {
		if !strings.HasPrefix(value, encryptedSecretPrefix) {
			t.Fatalf("%s was stored as plaintext: %q", name, value)
		}
	}

	credential, err := store.GetServerCredential(id)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AuthPassword != "server-password" || credential.AuthPrivateKey != "private-key" || credential.AuthPrivateKeyPassphrase != "key-password" {
		t.Fatalf("credential did not decrypt correctly: %#v", credential)
	}
}

func TestOpenStoreMigratesPlaintextSecrets(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO server_credentials(label, target_user, auth_type, auth_password) VALUES('old', 'root', 'password', 'plaintext')`); err != nil {
		t.Fatal(err)
	}
	store.db.Close()

	store, err = OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	var raw string
	if err := store.db.QueryRow(`SELECT auth_password FROM server_credentials WHERE label = 'old'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "plaintext" || !strings.HasPrefix(raw, encryptedSecretPrefix) {
		t.Fatalf("plaintext secret was not migrated: %q", raw)
	}
	credential, err := store.GetServerCredential(1)
	if err != nil || credential.AuthPassword != "plaintext" {
		t.Fatalf("migrated credential is unreadable: credential=%#v err=%v", credential, err)
	}
	if info, err := os.Stat(dbPath + ".key"); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		t.Fatalf("encryption key permissions are not 0600: info=%v err=%v", info, err)
	}
}

func TestForeignKeysCascadeServerRelations(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertServer(ServerRecord{ProxyUser: "server-a", TargetHost: "127.0.0.1", TargetPort: 22}); err != nil {
		t.Fatal(err)
	}
	credentialID, err := store.CreateClientCredential(ClientCredential{
		Label: "agent", AuthType: "password", Password: "password",
	}, []string{"server-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteServer("server-a"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM server_client_credentials WHERE client_credential_id = ?`, credentialID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected relation to be cascade-deleted, got %d rows", count)
	}
}

func TestBulkUpdateServerCredentialOperations(t *testing.T) {
	store := openTestStore(t)
	credentialA, err := store.CreateServerCredential(ServerCredential{
		Label: "credential-a", TargetUser: "root", AuthType: "password", AuthPassword: "password-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentialB, err := store.CreateServerCredential(ServerCredential{
		Label: "credential-b", TargetUser: "root", AuthType: "password", AuthPassword: "password-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range []ServerRecord{
		{ProxyUser: "server-a", TargetHost: "10.0.0.1", TargetPort: 22},
		{ProxyUser: "server-b", TargetHost: "10.0.0.2", TargetPort: 22, ServerCredentialID: &credentialA},
		{ProxyUser: "windows-a", TargetHost: "Windows Agent", ConnectionType: "agent"},
	} {
		if err := store.UpsertServer(server); err != nil {
			t.Fatal(err)
		}
	}
	serverA, _ := store.GetServer("server-a")
	serverB, _ := store.GetServer("server-b")
	windows, _ := store.GetServer("windows-a")

	if err := store.BulkUpdateServerCredential([]int64{serverA.ID, serverB.ID}, credentialB, bulkCredentialReplace); err != nil {
		t.Fatal(err)
	}
	if err := store.BulkUpdateServerCredential([]int64{serverA.ID, serverB.ID}, 0, bulkCredentialRemove); err != nil {
		t.Fatal(err)
	}
	serverA, _ = store.GetServer("server-a")
	serverB, _ = store.GetServer("server-b")
	if serverA.ServerCredentialID != nil || serverB.ServerCredentialID != nil {
		t.Fatalf("remove did not clear matching credentials: server-a=%v server-b=%v", serverA.ServerCredentialID, serverB.ServerCredentialID)
	}
	if err := store.BulkUpdateServerCredential([]int64{serverA.ID}, credentialA, bulkCredentialAdd); err == nil {
		t.Fatal("expected add operation to be rejected for single-value server credentials")
	}

	if err := store.BulkUpdateServerCredential([]int64{serverA.ID, windows.ID}, credentialA, bulkCredentialReplace); err == nil {
		t.Fatal("expected Agent server validation to fail")
	}
	serverA, _ = store.GetServer("server-a")
	if serverA.ServerCredentialID != nil {
		t.Fatalf("failed bulk operation was not rolled back: %v", serverA.ServerCredentialID)
	}
}

func TestBulkUpdateClientCredentialOperationsAndRollback(t *testing.T) {
	store := openTestStore(t)
	for _, server := range []ServerRecord{
		{ProxyUser: "server-a", TargetHost: "10.0.0.1", TargetPort: 22},
		{ProxyUser: "server-b", TargetHost: "10.0.0.2", TargetPort: 22},
	} {
		if err := store.UpsertServer(server); err != nil {
			t.Fatal(err)
		}
	}
	serverA, _ := store.GetServer("server-a")
	serverB, _ := store.GetServer("server-b")
	credentialA, err := store.CreateClientCredential(ClientCredential{
		Label: "client-a", AuthType: "password", Password: "password-a",
	}, []string{"server-a"})
	if err != nil {
		t.Fatal(err)
	}
	credentialB, err := store.CreateClientCredential(ClientCredential{
		Label: "client-b", AuthType: "password", Password: "password-b",
	}, []string{"server-b"})
	if err != nil {
		t.Fatal(err)
	}

	serverIDs := []int64{serverA.ID, serverB.ID}
	if err := store.BulkUpdateClientCredentials(serverIDs, []int64{credentialA}, bulkCredentialAdd); err != nil {
		t.Fatal(err)
	}
	assertClientCredentialIDs(t, store, "server-a", credentialA)
	assertClientCredentialIDs(t, store, "server-b", credentialA, credentialB)

	if err := store.BulkUpdateClientCredentials(serverIDs, []int64{credentialA}, bulkCredentialRemove); err != nil {
		t.Fatal(err)
	}
	assertClientCredentialIDs(t, store, "server-a")
	assertClientCredentialIDs(t, store, "server-b", credentialB)

	if err := store.BulkUpdateClientCredentials(serverIDs, []int64{credentialA}, bulkCredentialReplace); err != nil {
		t.Fatal(err)
	}
	assertClientCredentialIDs(t, store, "server-a", credentialA)
	assertClientCredentialIDs(t, store, "server-b", credentialA)

	if err := store.BulkUpdateClientCredentials(serverIDs, []int64{credentialB, 99999}, bulkCredentialAdd); err == nil {
		t.Fatal("expected invalid credential validation to fail")
	}
	assertClientCredentialIDs(t, store, "server-a", credentialA)
	assertClientCredentialIDs(t, store, "server-b", credentialA)
}

func assertClientCredentialIDs(t *testing.T, store *Store, proxyUser string, expected ...int64) {
	t.Helper()
	credentials, err := store.ListClientCredentialsForServer(proxyUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != len(expected) {
		t.Fatalf("%s credentials=%v, expected IDs=%v", proxyUser, credentials, expected)
	}
	actual := make(map[int64]bool, len(credentials))
	for _, credential := range credentials {
		actual[credential.ID] = true
	}
	for _, id := range expected {
		if !actual[id] {
			t.Fatalf("%s credentials=%v, expected ID=%d", proxyUser, credentials, id)
		}
	}
}

func TestAuditFiltersCanBeCombined(t *testing.T) {
	store := openTestStore(t)
	logs := []AuditLog{
		{ProxyUser: "alpha", TargetHost: "10.0.0.1", TargetPort: 22, EventType: "exec", Status: "completed", ClientCredentialLabel: "client-a"},
		{ProxyUser: "alpha", TargetHost: "10.0.0.2", TargetPort: 22, EventType: "exec", Status: "completed", ClientCredentialLabel: "client-b"},
		{ProxyUser: "beta", TargetHost: "10.0.0.1", TargetPort: 22, EventType: "exec", Status: "completed", ClientCredentialLabel: "client-a"},
	}
	for _, entry := range logs {
		if _, err := store.InsertAuditLog(entry); err != nil {
			t.Fatal(err)
		}
	}
	filtered, err := store.ListAuditLogs(200, AuditFilters{ProxyUser: "alpha", TargetHost: "10.0.0.1", ClientCredentialLabel: "client-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ProxyUser != "alpha" || filtered[0].TargetHost != "10.0.0.1" {
		t.Fatalf("unexpected filtered logs: %#v", filtered)
	}
}

func TestLegacyServerSchemaMigrationPreservesRelations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE servers (
			proxy_user TEXT PRIMARY KEY, target_host TEXT NOT NULL, target_port INTEGER NOT NULL DEFAULT 22,
			enabled INTEGER NOT NULL DEFAULT 1, server_credential_id INTEGER, last_test_at DATETIME,
			last_test_ok INTEGER, last_test_error TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE client_credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT, label TEXT NOT NULL, auth_type TEXT NOT NULL,
			public_key TEXT, password_hash TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE server_client_credentials (
			proxy_user TEXT NOT NULL REFERENCES servers(proxy_user) ON DELETE CASCADE,
			client_credential_id INTEGER NOT NULL REFERENCES client_credentials(id) ON DELETE CASCADE,
			PRIMARY KEY (proxy_user, client_credential_id))`,
		`INSERT INTO servers(proxy_user, target_host, target_port) VALUES('legacy', '10.0.0.1', 22)`,
		`INSERT INTO client_credentials(id, label, auth_type, public_key) VALUES(1, 'agent', 'public_key', 'key')`,
		`INSERT INTO server_client_credentials(proxy_user, client_credential_id) VALUES('legacy', 1)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	db.Close()

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	server, err := store.GetServer("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if server.ID == 0 || len(server.ClientCredentialLabels) != 1 || server.ClientCredentialLabels[0] != "agent" {
		t.Fatalf("legacy relation was not preserved: %#v", server)
	}
}

func TestResolveDynamicPortServer(t *testing.T) {
	store := openTestStore(t)
	dynamic := ServerRecord{
		ProxyUser: "pcdn-${PORT}", TargetHost: "192.168.1.100", RouteMode: "dynamic_port",
		PortMin: 8000, PortMax: 9000,
	}
	if err := store.UpsertServer(dynamic); err != nil {
		t.Fatal(err)
	}
	credentialID, err := store.CreateClientCredential(ClientCredential{
		Label: "pcdn-agent", AuthType: "password", Password: "password",
	}, []string{"pcdn-${PORT}"})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := store.ResolveServer("pcdn-8888")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ProxyUser != "pcdn-8888" || resolved.TargetHost != "192.168.1.100" || resolved.TargetPort != 8888 {
		t.Fatalf("unexpected dynamic route: %#v", resolved)
	}
	credentials, err := store.ListClientCredentialsForServer("pcdn-8888")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].ID != credentialID {
		t.Fatalf("dynamic route did not inherit client credentials: %#v", credentials)
	}
	for _, login := range []string{"pcdn-7999", "pcdn-9001", "pcdn-08888", "pcdn-not-a-port"} {
		if _, err := store.ResolveServer(login); err == nil {
			t.Fatalf("invalid dynamic login %q was accepted", login)
		}
	}
}

func TestFixedRouteTakesPriorityOverDynamicPortRule(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertServer(ServerRecord{
		ProxyUser: "pcdn-${PORT}", TargetHost: "192.168.1.100", RouteMode: "dynamic_port", PortMin: 1, PortMax: 65535,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertServer(ServerRecord{
		ProxyUser: "pcdn-8888", TargetHost: "192.168.1.101", TargetPort: 22, RouteMode: "fixed",
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveServer("pcdn-8888")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TargetHost != "192.168.1.101" || resolved.TargetPort != 22 {
		t.Fatalf("fixed route did not take priority: %#v", resolved)
	}
}

func TestLongestDynamicPortPrefixWins(t *testing.T) {
	store := openTestStore(t)
	for _, server := range []ServerRecord{
		{ProxyUser: "pcdn-${PORT}", TargetHost: "192.168.1.100", RouteMode: "dynamic_port", PortMin: 1, PortMax: 65535},
		{ProxyUser: "pcdn-edge-${PORT}", TargetHost: "192.168.1.101", RouteMode: "dynamic_port", PortMin: 1, PortMax: 65535},
	} {
		if err := store.UpsertServer(server); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := store.ResolveServer("pcdn-edge-8888")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TargetHost != "192.168.1.101" {
		t.Fatalf("longest dynamic prefix did not win: %#v", resolved)
	}
}
