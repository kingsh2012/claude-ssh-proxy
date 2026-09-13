package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingsh2012/ops-ssh-proxy/internal/winagent"
	"golang.org/x/crypto/ssh"
)

func TestSharedWindowsAgentCustomNameReadsFileThroughSSH(t *testing.T) {
	p, a, session, cid := enrollmentFixture(t)
	secret, _ := createSharedToken(t, a, session, cid)
	if err := p.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	h := httptest.NewServer(http.HandlerFunc(p.agents.ServeHTTP))
	defer h.Close()
	path := filepath.Join(t.TempDir(), "中文日志.log")
	if err := os.WriteFile(path, []byte("共享密钥自注册文件读取成功\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- winagent.Connect(ctx, winagent.Config{ServerURL: "ws" + strings.TrimPrefix(h.URL, "http") + "/agent", Token: secret, Hostname: "es-custom-login"})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("agent did not stop")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, _ := p.store.GetServer("es-custom-login")
		if s != nil && p.agents.Online(s.ID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("custom agent registration failed")
		}
		time.Sleep(time.Millisecond)
	}
	client, err := ssh.Dial("tcp", p.listener.Addr().String(), &ssh.ClientConfig{User: "es-custom-login", Auth: []ssh.AuthMethod{ssh.Password("test-password")}, HostKeyCallback: ssh.FixedHostKey(p.hostSigner.PublicKey()), Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cmd, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Close()
	out, err := cmd.CombinedOutput(fmt.Sprintf("Get-Content -LiteralPath '%s' -Encoding UTF8", strings.ReplaceAll(path, "'", "''")))
	if err != nil || !strings.Contains(string(out), "共享密钥自注册文件读取成功") {
		t.Fatalf("shared-key SSH file read failed: %v", err)
	}
}

func TestRealWindowsAgentReadsLogThroughSSH(t *testing.T) {
	p, r, token, h := agentFixture(t)
	path := filepath.Join(t.TempDir(), "中文日志.log")
	if err := os.WriteFile(path, []byte("first\nES 恢复完成\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- winagent.Connect(ctx, winagent.Config{ServerURL: "ws" + strings.TrimPrefix(h.URL, "http") + "/agent", ID: r.ID, Token: token})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Agent did not stop")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !p.agents.Online(r.ID) {
		if time.Now().After(deadline) {
			t.Fatal("Agent offline")
		}
		time.Sleep(time.Millisecond)
	}
	client := dialTestSSH(t, p)
	for i := 0; i < 3; i++ {
		s, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		out, err := s.CombinedOutput(fmt.Sprintf("Get-Content -LiteralPath '%s' -Encoding UTF8 -Tail 1", strings.ReplaceAll(path, "'", "''")))
		s.Close()
		if err != nil || !strings.Contains(string(out), "ES 恢复完成") {
			t.Fatalf("read log failed: %s / %v", out, err)
		}
	}
	// Exercise bounded streaming and cancellation, not just tiny outputs.
	s, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	err = s.Run(`[Console]::Write('x' * 9000000)`)
	s.Close()
	exit, ok := err.(*ssh.ExitError)
	if !ok || exit.ExitStatus() != 125 {
		t.Fatalf("output limit did not stop task: %v", err)
	}
}
