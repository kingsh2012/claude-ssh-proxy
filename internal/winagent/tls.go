package winagent

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Set at build time with -ldflags -X. Only public CA certificates belong here.
var embeddedCABase64 string

func clientTLSConfig(encodedCA string) (*tls.Config, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("无法定位客户端程序")
	}
	return clientTLSConfigForExecutable(encodedCA, executable)
}

func clientTLSConfigForExecutable(encodedCA, executable string) (*tls.Config, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if encodedCA != "" {
		certs, err := base64.StdEncoding.DecodeString(encodedCA)
		if err != nil || !roots.AppendCertsFromPEM(certs) {
			return nil, errors.New("客户端内置CA无效，请使用有效CA证书重新构建客户端")
		}
	}
	externalCA, err := os.ReadFile(clientCAPath(executable))
	if err == nil {
		if !roots.AppendCertsFromPEM(externalCA) {
			return nil, errors.New("客户端外部CA无效，请检查客户端旁边的CA证书")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("无法读取客户端外部CA：%w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, nil
}

func clientCAPath(executable string) string {
	extension := filepath.Ext(executable)
	name := strings.TrimSuffix(filepath.Base(executable), extension) + "-ca.crt"
	return filepath.Join(filepath.Dir(executable), name)
}

func preserveEmbeddedCA(executable string) error {
	if embeddedCABase64 == "" {
		return nil
	}
	path := clientCAPath(executable)
	if data, err := os.ReadFile(path); err == nil {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return errors.New("客户端旁边已有无效CA证书，未执行升级")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("无法检查客户端CA证书：%w", err)
	}
	data, err := base64.StdEncoding.DecodeString(embeddedCABase64)
	if err != nil || !x509.NewCertPool().AppendCertsFromPEM(data) {
		return errors.New("客户端内置CA无效，未执行升级")
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("无法保存客户端CA公钥证书：%w", err)
	}
	return nil
}
