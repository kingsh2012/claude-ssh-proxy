package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"golang.org/x/crypto/ssh/knownhosts"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSSHAuthorizationFeedbackAndMethods(t *testing.T) {
	newSigner := func() ssh.Signer {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.NewSignerFromKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return signer
	}
	allowed, other, host := newSigner(), newSigner(), newSigner()
	tests := []struct {
		name            string
		keys            []ssh.Signer
		passwordEnabled bool
		password        string
		target          string
		success         bool
		banner          bool
		prompt          bool
	}{
		{name: "authorized key", keys: []ssh.Signer{allowed}, target: "host", success: true},
		{name: "try next key", keys: []ssh.Signer{other, allowed}, target: "host", success: true, banner: true},
		{name: "unauthorized key without password prompt", keys: []ssh.Signer{other}, target: "host", banner: true},
		{name: "no key cannot bypass authentication", target: "host"},
		{name: "password remains available when authorized", keys: []ssh.Signer{other}, target: "host", passwordEnabled: true, password: "client-password", success: true, banner: true, prompt: true},
		{name: "wrong password rejected", target: "host", passwordEnabled: true, password: "wrong", banner: true, prompt: true},
		{name: "unbound host", keys: []ssh.Signer{allowed}, target: "unbound", banner: true},
		{name: "unknown host", keys: []ssh.Signer{allowed}, target: "missing", banner: true},
		{name: "disabled host", keys: []ssh.Signer{allowed}, target: "disabled", banner: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			for _, name := range []string{"host", "unbound", "disabled"} {
				if err := store.UpsertServer(ServerRecord{ProxyUser: name, TargetHost: "127.0.0.1", TargetPort: 22}); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SetServerEnabled("disabled", false); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateClientCredential(ClientCredential{Label: "authorized", AuthType: "public_key", PublicKey: string(ssh.MarshalAuthorizedKey(allowed.PublicKey()))}, []string{"host", "disabled"}); err != nil {
				t.Fatal(err)
			}
			if test.passwordEnabled {
				if _, err := store.CreateClientCredential(ClientCredential{Label: "password", AuthType: "password", Password: "client-password"}, []string{"host"}); err != nil {
					t.Fatal(err)
				}
			}
			proxy := &Proxy{store: store, hostSigner: host}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				server, _, _, err := ssh.NewServerConn(conn, proxy.newServerConfig())
				if server != nil {
					server.Close()
				}
				done <- err
			}()
			var banners strings.Builder
			prompted := false
			methods := []ssh.AuthMethod{}
			if len(test.keys) > 0 {
				methods = append(methods, ssh.PublicKeys(test.keys...))
			}
			methods = append(methods, ssh.PasswordCallback(func() (string, error) { prompted = true; return test.password, nil }))
			conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			client, _, _, clientErr := ssh.NewClientConn(conn, listener.Addr().String(), &ssh.ClientConfig{
				User: test.target, Auth: methods, HostKeyCallback: ssh.FixedHostKey(host.PublicKey()),
				BannerCallback: func(message string) error { banners.WriteString(message); return nil },
			})
			if client != nil {
				client.Close()
			}
			conn.Close()
			serverErr := <-done
			if (clientErr == nil) != test.success || (serverErr == nil) != test.success {
				t.Fatalf("success=%v client=%v server=%v", test.success, clientErr, serverErr)
			}
			if prompted != test.prompt {
				t.Fatalf("password prompted=%v want=%v", prompted, test.prompt)
			}
			if test.banner && !strings.Contains(banners.String(), "SSH Proxy：") {
				t.Fatalf("missing authorization banner: %q", banners.String())
			}
		})
	}
}

func TestOpenSSHShowsAuthorizationBannerWithoutPasswordFallback(t *testing.T) {
	executable, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("OpenSSH not installed")
	}
	store := openTestStore(t)
	if err := store.UpsertServer(ServerRecord{ProxyUser: "host", TargetHost: "127.0.0.1", TargetPort: 22}); err != nil {
		t.Fatal(err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	identity := filepath.Join(directory, "identity")
	if err := os.WriteFile(identity, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	known := filepath.Join(directory, "known_hosts")
	if err := os.WriteFile(known, []byte(knownhosts.Line([]string{listener.Addr().String()}, signer.PublicKey())+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	proxy := &Proxy{store: store, hostSigner: signer}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		_, _, _, err = ssh.NewServerConn(conn, proxy.newServerConfig())
		done <- err
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, clientErr := exec.CommandContext(ctx, executable, "-F", "none", "-i", identity, "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+known, "-p", port, "host@"+host).CombinedOutput()
	listener.Close()
	if clientErr == nil {
		t.Fatal("unauthorized OpenSSH login succeeded")
	}
	if err := <-done; err == nil {
		t.Fatal("server accepted unauthenticated connection")
	}
	if !strings.Contains(string(output), "SSH Proxy：") || !strings.Contains(string(output), "(publickey)") {
		t.Fatalf("unexpected OpenSSH feedback: %s", output)
	}
	if strings.Contains(string(output), "(publickey,password)") {
		t.Fatalf("password was offered: %s", output)
	}
}
