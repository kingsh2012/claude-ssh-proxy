package main

import (
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestAuditSessionInsertsOnlyOnceUnderConcurrentRequests(t *testing.T) {
	store := openTestStore(t)
	audit := newAuditSession(store, "alpha", "192.0.2.1:1234", "10.0.0.1", 22, "agent-a")
	payload := ssh.Marshal(struct{ Command string }{Command: "whoami"})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			audit.noteRequest(&ssh.Request{Type: "exec", Payload: payload})
		}()
	}
	wg.Wait()
	audit.finish()

	logs, err := store.ListAuditLogs(200, AuditFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected exactly one audit row, got %d", len(logs))
	}
	if logs[0].Command != "whoami" || logs[0].Status != "completed" {
		t.Fatalf("unexpected audit row: %#v", logs[0])
	}
}
