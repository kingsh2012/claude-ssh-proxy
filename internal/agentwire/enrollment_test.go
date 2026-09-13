package agentwire

import (
	"strings"
	"testing"
)

func TestEnrollmentTokenCarriesOnlyValidWSSDestination(t *testing.T) {
	secret := strings.Repeat("a", 64)
	for _, server := range []string{"wss://example.com/agent", "wss://192.168.102.7:18443/agent"} {
		u, key, err := ParseEnrollmentToken(EnrollmentToken(server, secret))
		if err != nil || u != server || key != secret {
			t.Fatal("token round trip failed")
		}
	}
	for _, token := range []string{
		"secret-marker", "ops1.invalid." + secret,
		EnrollmentToken("ws://example.com/agent", secret),
		EnrollmentToken("wss://user:secret-marker@example.com/agent", secret),
		EnrollmentToken("wss://example.com/agent?token=secret-marker", secret),
		EnrollmentToken("wss://example.com/agent", strings.Repeat("z", 64)),
		EnrollmentToken("wss://example.com/agent", strings.Repeat("a", 63)),
		strings.Repeat("x", 4097),
	} {
		_, _, err := ParseEnrollmentToken(token)
		if err == nil {
			t.Fatal("invalid token accepted")
		}
		if strings.Contains(err.Error(), "secret-marker") {
			t.Fatal("token leaked in diagnostics")
		}
	}
}
