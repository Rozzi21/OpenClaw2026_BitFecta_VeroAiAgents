package config

import (
	"strings"
	"testing"
)

// Cookie SameSite configuration (GO-P2-6).
//
// auth.parseSameSite recognises lax/none/strict and maps EVERYTHING else —
// including a typo — to SameSiteStrictMode. That fallback is silent, and a
// Strict guest cookie is not sent on the cross-site top-level navigation
// Google's OAuth callback performs, so the guest-order claim stops working with
// no error and no log. Config.Validate must therefore refuse unknown values at
// startup instead of letting the process boot with a policy nobody chose.

// validSameSiteConfig builds a config whose only interesting field is the pair
// of cookie SameSite policies. AppEnv stays "development" so the production-only
// invariants (JWT secret, database password) cannot mask the assertion.
func validSameSiteConfig(jwtSameSite, guestSameSite string) Config {
	return Config{
		AppEnv:              "development",
		JWTCookieSameSite:   jwtSameSite,
		GuestCookieSameSite: guestSameSite,
	}
}

func TestValidateAcceptsSupportedCookieSameSite(t *testing.T) {
	// Accepted exactly like auth.parseSameSite reads them: case-insensitive and
	// whitespace-tolerant. "" means the env var was not set, so Load's default
	// applies.
	for _, value := range []string{"Strict", "Lax", "None", "strict", "lax", "none", "LAX", " Lax ", ""} {
		if err := validSameSiteConfig(value, "Lax").Validate(); err != nil {
			t.Fatalf("JWT_COOKIE_SAME_SITE=%q must be accepted: %v", value, err)
		}
		if err := validSameSiteConfig("Strict", value).Validate(); err != nil {
			t.Fatalf("GUEST_COOKIE_SAME_SITE=%q must be accepted: %v", value, err)
		}
	}
}

func TestValidateRejectsInvalidGuestCookieSameSite(t *testing.T) {
	// Typos, close-but-wrong spellings, and values from other config formats.
	// None of them may boot the app with a silently tightened cookie.
	for _, value := range []string{"Laxx", "lx", "Stric", "Strictly", "SameSite=Lax", "no", "true", "0", "Lax;", "Lax Strict", "-"} {
		err := validSameSiteConfig("Lax", value).Validate()
		if err == nil {
			t.Fatalf("GUEST_COOKIE_SAME_SITE=%q must fail configuration validation", value)
		}
		if !strings.Contains(err.Error(), "GUEST_COOKIE_SAME_SITE") {
			t.Fatalf("error must name the offending variable, got %q", err.Error())
		}
	}
}

func TestValidateRejectsInvalidJWTCookieSameSite(t *testing.T) {
	err := validSameSiteConfig("Laxx", "Lax").Validate()
	if err == nil {
		t.Fatal("JWT_COOKIE_SAME_SITE=Laxx must fail configuration validation")
	}
	if !strings.Contains(err.Error(), "JWT_COOKIE_SAME_SITE") {
		t.Fatalf("error must name the offending variable, got %q", err.Error())
	}
}

// TestValidateRejectsInvalidCookieSameSiteInEveryEnvironment: the check is not
// production-gated. A developer/staging deployment with the typo would otherwise
// "work" until someone tried to claim a guest order after a Google login.
func TestValidateRejectsInvalidCookieSameSiteInEveryEnvironment(t *testing.T) {
	for _, env := range []string{"development", "staging", "production"} {
		cfg := validSameSiteConfig("Lax", "Laxx")
		cfg.AppEnv = env
		// Production credentials are present so the SameSite error is the only
		// possible reason for the failure.
		cfg.DatabasePassword = "a-strong-development-password"
		cfg.JWTSecret = "a-strong-development-secret"
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("APP_ENV=%s must still reject an invalid SameSite value", env)
		}
		if !strings.Contains(err.Error(), "GUEST_COOKIE_SAME_SITE") {
			t.Fatalf("APP_ENV=%s: unexpected failure reason %q", env, err.Error())
		}
	}
}

// TestLoadThenValidateRejectsGuestCookieSameSiteTypo exercises the real startup
// path (Load reads the environment, main calls Validate): a typo must stop the
// process, and the documented default must still pass.
func TestLoadThenValidateRejectsGuestCookieSameSiteTypo(t *testing.T) {
	// Pin the rest of the environment so the SameSite value is the only variable
	// (an ambient APP_ENV=production would add unrelated invariants).
	t.Setenv("APP_ENV", "development")
	t.Setenv("JWT_COOKIE_SAME_SITE", "")
	t.Setenv("GUEST_COOKIE_SAME_SITE", "Laxx")
	if err := Load().Validate(); err == nil {
		t.Fatal("Load+Validate must reject GUEST_COOKIE_SAME_SITE=Laxx")
	}

	t.Setenv("GUEST_COOKIE_SAME_SITE", "Lax")
	if err := Load().Validate(); err != nil {
		t.Fatalf("Load+Validate must accept GUEST_COOKIE_SAME_SITE=Lax: %v", err)
	}
}

// TestLoadDefaultsAreValid pins that the shipped defaults (Lax for the guest
// cookie, Strict for the refresh cookie) pass their own validation.
func TestLoadDefaultsAreValid(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("JWT_COOKIE_SAME_SITE", "")
	t.Setenv("GUEST_COOKIE_SAME_SITE", "")
	cfg := Load()
	if cfg.GuestCookieSameSite != "Lax" {
		t.Fatalf("guest cookie default changed to %q — the Google claim needs Lax or None", cfg.GuestCookieSameSite)
	}
	if cfg.JWTCookieSameSite != "Strict" {
		t.Fatalf("refresh cookie default changed to %q", cfg.JWTCookieSameSite)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default configuration must validate: %v", err)
	}
}
