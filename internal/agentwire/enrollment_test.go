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

func TestHTTPSOriginCompatibility(t *testing.T) {
	secret := strings.Repeat("b", 64)
	for _, value := range []string{"https://example.com", "https://example.com/", "https://example.com:8443"} {
		expected := strings.Replace(strings.TrimSuffix(value, "/"), "https://", "wss://", 1) + "/agent"
		got, err := AgentServerURL(value)
		if err != nil || got != expected {
			t.Fatalf("HTTPS conversion failed: %v", err)
		}
		destination, key, err := ParseEnrollmentToken(EnrollmentToken(value, secret))
		if err != nil || destination != expected || key != secret {
			t.Fatal("HTTPS token failed")
		}
	}
	for _, value := range []string{"http://example.com", "https://example.com/download", "https://example.com/agent", "https://user:pass@example.com", "https://example.com?token=secret", "https://example.com/#fragment", "https://example.com/%2f", "https://example.com?"} {
		if _, err := AgentServerURL(value); err == nil {
			t.Fatal("invalid public origin accepted")
		}
	}
}
