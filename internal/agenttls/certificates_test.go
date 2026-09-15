package agenttls

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateIPCertificateReuseCAAndRefuseOverwrite(t *testing.T) {
	root := t.TempDir()
	caDir := filepath.Join(root, "offline-ca")
	out := filepath.Join(root, "server")
	ip := net.ParseIP("192.0.2.7")
	if err := Generate(caDir, out, []net.IP{ip}, nil); err != nil {
		t.Fatal(err)
	}
	caBefore, err := os.ReadFile(filepath.Join(caDir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	certPEM, err := os.ReadFile(filepath.Join(out, "server.crt"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caBefore)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: ip.String()}); err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("192.0.2.8"); err == nil {
		t.Fatal("wrong IP accepted")
	}
	if _, err := os.Stat(filepath.Join(out, "ca.key")); !os.IsNotExist(err) {
		t.Fatal("CA private key copied to server directory")
	}
	if err := Generate(caDir, out, []net.IP{ip}, nil); err == nil {
		t.Fatal("existing server identity overwritten")
	}
	if err := Generate(caDir, filepath.Join(root, "renewed"), []net.IP{ip}, nil); err != nil {
		t.Fatal(err)
	}
	caAfter, _ := os.ReadFile(filepath.Join(caDir, "ca.crt"))
	if string(caBefore) != string(caAfter) {
		t.Fatal("CA changed on renewal")
	}
	if err := Generate(caDir, caDir, []net.IP{ip}, nil); err == nil {
		t.Fatal("CA and server directory can be the same")
	}
}
