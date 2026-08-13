package main

import (
	"crypto/ed25519"
	"crypto/rand"
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
