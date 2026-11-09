package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestRestartFailureKeepsOldListenerAvailable(t *testing.T) {
	store := openTestStore(t)
	proxy, err := NewProxy(store, filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop()

	proxy.mu.Lock()
	oldAddr := proxy.listener.Addr().String()
	proxy.mu.Unlock()

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if err := proxy.Restart(occupied.Addr().String()); err == nil {
		t.Fatal("expected restart to an occupied address to fail")
	}

	connection, err := net.DialTimeout("tcp", oldAddr, time.Second)
	if err != nil {
		t.Fatalf("old listener was unavailable after failed restart: %v", err)
	}
	connection.Close()
}

func TestVerifyHostKeyFingerprint(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := ssh.FingerprintSHA256(publicKey)
	callback := verifyHostKeyFingerprint(fingerprint)
	if err := callback("server", &net.TCPAddr{}, publicKey); err != nil {
		t.Fatalf("matching fingerprint was rejected: %v", err)
	}
	callback = verifyHostKeyFingerprint("SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err := callback("server", &net.TCPAddr{}, publicKey); err == nil {
		t.Fatal("mismatching fingerprint was accepted")
	}
}

func TestRestartSwitchesToNewListener(t *testing.T) {
	store := openTestStore(t)
	proxy, err := NewProxy(store, filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop()

	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	newAddr := reserved.Addr().String()
	reserved.Close()
	if err := proxy.Restart(newAddr); err != nil {
		t.Fatal(err)
	}
	proxy.mu.Lock()
	actualAddr := proxy.listener.Addr().String()
	proxy.mu.Unlock()
	if actualAddr != newAddr {
		t.Fatalf("listener address mismatch: got %s, want %s", actualAddr, newAddr)
	}
	connection, err := net.DialTimeout("tcp", actualAddr, time.Second)
	if err != nil {
		t.Fatalf("new listener was unavailable: %v", err)
	}
	connection.Close()
}

func TestExecSessionClosesAfterUpstreamExit(t *testing.T) {
	upstreamHost, upstreamPort := startExecTestServer(t)
	store := openTestStore(t)
	serverCredentialID, err := store.CreateServerCredential(ServerCredential{
		Label: "upstream", TargetUser: "root", AuthType: "password", AuthPassword: "upstream-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertServer(ServerRecord{
		ProxyUser: "exec-test", TargetHost: upstreamHost, TargetPort: upstreamPort,
		ServerCredentialID: &serverCredentialID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateClientCredential(ClientCredential{
		Label: "client", AuthType: "password", Password: "client-password",
	}, []string{"exec-test"}); err != nil {
		t.Fatal(err)
	}

	proxy, err := NewProxy(store, filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop()

	proxy.mu.Lock()
	proxyAddr := proxy.listener.Addr().String()
	proxy.mu.Unlock()
	client, err := ssh.Dial("tcp", proxyAddr, &ssh.ClientConfig{
		User: "exec-test", Auth: []ssh.AuthMethod{ssh.Password("client-password")},
		HostKeyCallback: ssh.FixedHostKey(proxy.hostSigner.PublicKey()), Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	channel, requests, err := client.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	requestsClosed := make(chan struct{})
	go func() {
		for range requests {
		}
		close(requestsClosed)
	}()
	if ok, err := channel.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{Command: "hostname"})); err != nil || !ok {
		t.Fatalf("exec request failed: ok=%t err=%v", ok, err)
	}

	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 1)
	go func() {
		// Keep the downstream write side open. OpenSSH can retain stdin this way
		// even after the remote command has sent exit-status and closed output.
		output, err := io.ReadAll(channel)
		done <- result{output: output, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("exec failed: %v", got.err)
		}
		if string(got.output) != "exec-complete\n" {
			t.Fatalf("unexpected output %q", got.output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec session did not close after upstream exit-status")
	}
	select {
	case <-requestsClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("exec channel remained open after upstream exit-status")
	}

	client.Close()
	deadline := time.Now().Add(2 * time.Second)
	for len(proxy.ActiveConnections()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if connections := proxy.ActiveConnections(); len(connections) != 0 {
		t.Fatalf("proxy retained active connections after client close: %#v", connections)
	}
}

func startExecTestServer(t *testing.T) (string, int) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if string(password) != "upstream-password" {
			return nil, fmt.Errorf("invalid password")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		serverConnection, channels, requests, err := ssh.NewServerConn(connection, config)
		if err != nil {
			return
		}
		defer serverConnection.Close()
		go ssh.DiscardRequests(requests)
		for newChannel := range channels {
			if newChannel.ChannelType() != "session" {
				newChannel.Reject(ssh.UnknownChannelType, "unsupported")
				continue
			}
			channel, channelRequests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go func() {
				defer channel.Close()
				for request := range channelRequests {
					if request.Type != "exec" {
						request.Reply(false, nil)
						continue
					}
					request.Reply(true, nil)
					channel.Write([]byte("exec-complete\n"))
					channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
					channel.CloseWrite()
					return
				}
			}()
		}
	}()
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}
