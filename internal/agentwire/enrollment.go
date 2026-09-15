package agentwire

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

func ValidServerURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && len(value) <= 2048 && u.Scheme == "wss" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "/agent"
}

// AgentServerURL accepts the public HTTPS origin or a legacy WSS endpoint.
func AgentServerURL(value string) (string, error) {
	if ValidServerURL(value) {
		return value, nil
	}
	u, err := url.Parse(value)
	if err != nil || len(value) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("请填写HTTPS主域名或WSS地址，例如https://proxy.example.com或wss://proxy.example.com/agent")
	}
	u.Scheme = "wss"
	u.Path = "/agent"
	return u.String(), nil
}

// The token carries its destination so the user only needs one CLI argument.
// The destination is public metadata; only the random secret authenticates.
func EnrollmentToken(serverURL, secret string) string {
	return "ops1." + base64.RawURLEncoding.EncodeToString([]byte(serverURL)) + "." + secret
}

func ParseEnrollmentToken(token string) (serverURL, secret string, err error) {
	parts := strings.Split(token, ".")
	if len(token) > 4096 || len(parts) != 3 || parts[0] != "ops1" {
		return "", "", errors.New("接入Token格式不正确，请从网页复制完整Token")
	}
	server, decodeErr := base64.RawURLEncoding.DecodeString(parts[1])
	key, keyErr := hex.DecodeString(parts[2])
	destination, addressErr := AgentServerURL(string(server))
	if decodeErr != nil || addressErr != nil || keyErr != nil || len(key) != 32 {
		return "", "", errors.New("接入Token地址或凭证无效")
	}
	return destination, parts[2], nil
}

var validHostname = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)

func ValidHostname(name string) bool { return validHostname.MatchString(name) }
