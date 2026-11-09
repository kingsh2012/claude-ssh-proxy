package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditSummaryLeavesLargeContentForDetail(t *testing.T) {
	store := openTestStore(t)
	content := strings.Repeat("large-output", 100000)
	var id int64
	for _, user := range []string{"alpha", "beta", "alpha"} {
		var err error
		id, err = store.InsertAuditLog(AuditLog{ProxyUser: user, TargetHost: "10.0.0.1", TargetPort: 22, EventType: "exec", Command: content, ClientCredentialLabel: "operator"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteAuditLog(id, content, content, nil, false); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := store.ListAuditSummaries(1, AuditFilters{ProxyUser: "alpha", TargetHost: "10.0.0.1", ClientCredentialLabel: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].ID != id {
		t.Fatalf("unexpected summary: %#v", logs)
	}
	if logs[0].Command != "" || logs[0].Output != "" || logs[0].Detail != "" {
		t.Fatal("summary loaded large fields")
	}
	detail, err := store.GetAuditLog(id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Command != content || detail.Output != content || detail.Detail != content {
		t.Fatal("detail content lost")
	}
	if _, err := store.GetAuditLog(id + 100); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing detail: %v", err)
	}
	full, err := store.ListAuditLogs(1, AuditFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 1 || full[0].Output != content {
		t.Fatal("legacy full-content API changed")
	}
	for _, column := range []string{"target_host", "client_credential_label"} {
		rows, err := store.db.Query("EXPLAIN QUERY PLAN SELECT id FROM audit_logs WHERE "+column+" = ? ORDER BY id DESC LIMIT 200", "operator")
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail)
		}
		rows.Close()
		if !strings.Contains(plan.String(), "USING COVERING INDEX") || strings.Contains(plan.String(), "TEMP B-TREE") {
			t.Fatalf("unindexed filter %s: %s", column, plan.String())
		}
	}
}

func TestAuditCursorReachesAllHistoryWithoutDeletingRows(t *testing.T) {
	store := openTestStore(t)
	for range 450 {
		if _, err := store.InsertAuditLog(AuditLog{ProxyUser: "history", TargetHost: "10.0.0.1", TargetPort: 22, EventType: "exec"}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListAuditSummaries(200, AuditFilters{ProxyUser: "history"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 200 {
		t.Fatalf("first batch size %d", len(first))
	}
	// New arrivals must not shift the boundary or duplicate older results.
	if _, err := store.InsertAuditLog(AuditLog{ProxyUser: "history", TargetHost: "10.0.0.1", TargetPort: 22, EventType: "exec"}); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	before := int64(0)
	batch := first
	for len(batch) > 0 {
		for _, entry := range batch {
			if seen[entry.ID] {
				t.Fatal("duplicate history entry")
			}
			seen[entry.ID] = true
		}
		before = batch[len(batch)-1].ID
		batch, err = store.ListAuditSummaries(200, AuditFilters{ProxyUser: "history", BeforeID: before})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 450 {
		t.Fatalf("lost history: %d", len(seen))
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 451 {
		t.Fatalf("rows deleted: %d", count)
	}
}

func TestAuditAPIProtectsDetailAndSupportsHistory(t *testing.T) {
	store := openTestStore(t)
	if err := store.CreateAdminUser("admin", "test-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminPassword("admin", "test-password"); err != nil {
		t.Fatal(err)
	}
	id, err := store.InsertAuditLog(AuditLog{ProxyUser: "history", TargetHost: "10.0.0.1", TargetPort: 22, EventType: "exec", Command: "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAuditLog(id, "root", "full transcript", nil, false); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, &Proxy{store: store})
	token, err := api.issueToken("admin")
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string, authorized bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if authorized {
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		}
		recorder := httptest.NewRecorder()
		api.Router().ServeHTTP(recorder, request)
		return recorder
	}
	path := fmt.Sprintf("/api/audit/%d", id)
	if r := get(path, false); r.Code != http.StatusUnauthorized {
		t.Fatalf("unprotected detail: %d", r.Code)
	}
	r := get(path, true)
	var detail AuditLog
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || detail.Output != "root" || detail.Command != "whoami" {
		t.Fatalf("detail missing: %s", r.Body)
	}
	r = get("/api/audit?summary=true", true)
	var summaries []AuditLog
	if err := json.Unmarshal(r.Body.Bytes(), &summaries); err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Output != "" {
		t.Fatal("list included detail")
	}
	r = get(fmt.Sprintf("/api/audit?summary=true&before_id=%d", id), true)
	if r.Code != 200 || strings.TrimSpace(r.Body.String()) != "[]" {
		t.Fatalf("cursor ignored: %s", r.Body)
	}
	for _, path := range []string{"/api/audit/0", "/api/audit/no-id", "/api/audit?before_id=-1", "/api/audit?before_id=bad"} {
		if r := get(path, true); r.Code != http.StatusBadRequest {
			t.Fatalf("invalid request accepted %s: %d", path, r.Code)
		}
	}
	if r := get(fmt.Sprintf("/api/audit/%d", id+100), true); r.Code != http.StatusNotFound {
		t.Fatalf("missing detail status: %d", r.Code)
	}
}
