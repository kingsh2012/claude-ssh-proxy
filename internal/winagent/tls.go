package winagent

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
)

// Set at build time with -ldflags -X. Only public CA certificates belong here.
var embeddedCABase64 string

func clientTLSConfig(encodedCA string) (*tls.Config, error) {
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
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, nil
}
