// Package agenttls creates deployment-specific certificates for the Agent listener.
package agenttls

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func serial() (*big.Int, error) { return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128)) }

func pemKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func loadCA(dir string) (*x509.Certificate, crypto.Signer, []byte, error) {
	certPEM, certErr := os.ReadFile(filepath.Join(dir, "ca.crt"))
	keyPEM, keyErr := os.ReadFile(filepath.Join(dir, "ca.key"))
	if os.IsNotExist(certErr) && os.IsNotExist(keyErr) {
		return nil, nil, nil, nil
	}
	if certErr != nil || keyErr != nil {
		return nil, nil, nil, errors.New("CA证书或私钥无法读取，拒绝覆盖或替换现有CA")
	}
	block, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if block == nil || block.Type != "CERTIFICATE" || keyBlock == nil || keyBlock.Type != "PRIVATE KEY" {
		return nil, nil, nil, errors.New("CA文件必须是PEM证书和PKCS8私钥")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, nil, nil, errors.New("CA证书无效")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, nil, errors.New("CA私钥无效")
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, nil, nil, errors.New("CA私钥不支持签名")
	}
	publicKey, ok := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !publicKey.Equal(signer.Public()) {
		return nil, nil, nil, errors.New("CA证书与私钥不匹配")
	}
	return cert, signer, certPEM, nil
}

// Generate keeps the CA private key in caDir and writes only the server identity to outDir.
// Existing CA identities are reused. Existing server files are never overwritten.
func Generate(caDir, outDir string, ips []net.IP, dnsNames []string) error {
	if caDir == "" || outDir == "" || len(ips)+len(dnsNames) == 0 {
		return errors.New("请指定CA目录、证书输出目录及公网IP或域名")
	}
	caAbs, err := filepath.Abs(caDir)
	if err != nil {
		return err
	}
	outAbs, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	if caAbs == outAbs {
		return errors.New("CA目录与服务器证书目录必须分开")
	}
	for _, name := range []string{"server.crt", "server.key"} {
		if _, err := os.Lstat(filepath.Join(outDir, name)); !os.IsNotExist(err) {
			return errors.New("输出目录已有服务器证书或私钥，请使用新的输出目录")
		}
	}
	for _, ip := range ips {
		if ip == nil {
			return errors.New("公网IP无效")
		}
	}
	now := time.Now()
	ca, caKey, caPEM, err := loadCA(caDir)
	if err != nil {
		return err
	}
	var newCAKey []byte
	if ca == nil {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		number, err := serial()
		if err != nil {
			return err
		}
		template := &x509.Certificate{SerialNumber: number, Subject: pkix.Name{CommonName: "aiagent deployment CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
		if err != nil {
			return err
		}
		ca, err = x509.ParseCertificate(der)
		if err != nil {
			return err
		}
		caKey = key
		caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		newCAKey, err = pemKey(key)
		if err != nil {
			return err
		}
	}
	expiry := now.AddDate(1, 0, 0)
	if expiry.After(ca.NotAfter) {
		expiry = ca.NotAfter
	}
	if now.Before(ca.NotBefore) || !expiry.After(now.Add(24*time.Hour)) {
		return errors.New("CA未生效或即将到期，无法签发服务器证书")
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	number, err := serial()
	if err != nil {
		return err
	}
	template := &x509.Certificate{SerialNumber: number, Subject: pkix.Name{CommonName: "aiagent listener"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: expiry, IPAddresses: ips, DNSNames: dnsNames, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, serverKey.Public(), caKey)
	if err != nil {
		return err
	}
	keyPEM, err := pemKey(serverKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(caDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0700); err != nil {
		return err
	}
	if newCAKey != nil {
		if err := writeNew(filepath.Join(caDir, "ca.key"), newCAKey, 0600); err != nil {
			return err
		}
		if err := writeNew(filepath.Join(caDir, "ca.crt"), caPEM, 0644); err != nil {
			return err
		}
	}
	if err := writeNew(filepath.Join(outDir, "server.key"), keyPEM, 0600); err != nil {
		return err
	}
	return writeNew(filepath.Join(outDir, "server.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644)
}
