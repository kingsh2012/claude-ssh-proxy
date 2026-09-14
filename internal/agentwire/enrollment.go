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

// The token carries its destination so the user only needs one CLI argument.
// The destination is public metadata; only the random secret authenticates.
func EnrollmentToken(serverURL, secret string) string {
	return "ops1." + base64.RawURLEncoding.EncodeToString([]byte(serverURL)) + "." + secret
}

func ParseEnrollmentToken(token string) (serverURL, secret string, err error) {
	parts := strings.Split(token, ".")
	if len(token) > 4096 || len(parts) != 3 || parts[0] != "ops1" {
		return "", "", errors.New("接入 Token 格式不正确，请从网页复制完整 Token")
	}
	server, decodeErr := base64.RawURLEncoding.DecodeString(parts[1])
	key, keyErr := hex.DecodeString(parts[2])
	if decodeErr != nil || !ValidServerURL(string(server)) || keyErr != nil || len(key) != 32 {
		return "", "", errors.New("接入 Token 地址或凭证无效")
	}
	return string(server), parts[2], nil
}

var validHostname = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)

func ValidHostname(name string) bool { return validHostname.MatchString(name) }
