package auth

import (
	"log/slog"
)

const (
	EventLoginSuccess              = "login_success"
	EventLoginFailed               = "login_failed"
	EventRefreshSuccess            = "refresh_success"
	EventRefreshFailed             = "refresh_failed"
	EventRefreshTokenRevoked       = "refresh_token_revoked"
	EventLogout                    = "logout"
	EventRefreshTokenUsedAsAccess  = "refresh_token_used_as_access"
	EventAccessTokenUsedOnRefresh  = "access_token_used_on_refresh"
	EventRefreshTokenReuseDetected = "refresh_token_reuse_detected"
	// Google OAuth (18 Agu 2026).
	EventGoogleLoginSuccess      = "google_login_success"
	EventGoogleLoginFailed       = "google_login_failed"
	EventGoogleOAuthStateInvalid = "google_oauth_state_invalid"
	EventGoogleAccountLinked     = "google_account_linked"
	EventGoogleAccountCreated    = "google_account_created"
)

func LogSecurity(event string, fields map[string]any) {
	args := []any{slog.String("security_event", event)}
	for key, value := range fields {
		args = append(args, slog.Any(key, value))
	}
	slog.Info("security_audit", args...)
}
