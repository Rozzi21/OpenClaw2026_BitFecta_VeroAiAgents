package middlewares

import (
	"strings"
	"testing"
)

// TestRedactSensitiveQuery locks the SEC-hardening (23 Agu 2026): OAuth
// artifacts and credentials in the URL query must never reach logs. The Google
// callback (?code=...&state=...) is the concrete leak being closed; the rest
// are defense-in-depth.
func TestRedactSensitiveQuery(t *testing.T) {
	t.Run("redacts google oauth callback code and state", func(t *testing.T) {
		got := redactSensitiveQuery("code=4/0AfJohXYZsecret&state=abc123&scope=email")
		if strings.Contains(got, "4/0AfJohXYZsecret") {
			t.Errorf("authorization code leaked: %s", got)
		}
		if strings.Contains(got, "abc123") {
			t.Errorf("state leaked: %s", got)
		}
		if !strings.Contains(got, "scope=email") {
			t.Errorf("non-sensitive key should be preserved: %s", got)
		}
		if !strings.Contains(got, "code=%5Bredacted%5D") && !strings.Contains(got, "code=[redacted]") {
			t.Errorf("code should be marked redacted: %s", got)
		}
	})

	t.Run("redacts all sensitive keys case-insensitively", func(t *testing.T) {
		secret := "supersecretvalue"
		keys := []string{"code", "state", "access_token", "refresh_token", "id_token", "token", "client_secret", "password",
			"CODE", "Access_Token", "Client_Secret"}
		for _, key := range keys {
			got := redactSensitiveQuery(key + "=" + secret)
			if strings.Contains(got, secret) {
				t.Errorf("key %q leaked secret: %s", key, got)
			}
		}
	})

	t.Run("preserves non-sensitive query", func(t *testing.T) {
		in := "return_to=%2Ftrip%2Fabc&page=2"
		got := redactSensitiveQuery(in)
		if !strings.Contains(got, "page=2") {
			t.Errorf("non-sensitive query lost: %s", got)
		}
	})

	t.Run("empty query stays empty", func(t *testing.T) {
		if got := redactSensitiveQuery(""); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("malformed query fails closed to empty", func(t *testing.T) {
		// A query that fails url.ParseQuery must not be echoed back (could
		// carry a secret). We return empty rather than the raw string.
		got := redactSensitiveQuery("code=%zz")
		if strings.Contains(got, "%zz") {
			t.Errorf("malformed query leaked raw: %s", got)
		}
	})
}
