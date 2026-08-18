package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
)

const refreshCookiePath = "/api/v1/auth"
const guestSessionCookieName = "vero_chat_session"
const guestSessionCookiePath = "/api/v1/chat"
const guestIdentityCookieName = "vero_guest_session"
const guestIdentityCookiePath = "/api/v1"

func SetRefreshCookie(c *gin.Context, cfg config.Config, token string, maxAgeSeconds int) {
	sameSite := parseSameSite(cfg.JWTCookieSameSite)
	c.SetSameSite(sameSite)
	// Browsers reject SameSite=None cookies unless they are also marked Secure,
	// so force Secure in that case even if JWT_COOKIE_SECURE was left false.
	secure := cfg.JWTCookieSecure
	if sameSite == http.SameSiteNoneMode {
		secure = true
	}
	c.SetCookie(
		cfg.JWTCookieName,
		token,
		maxAgeSeconds,
		refreshCookiePath,
		"",
		secure,
		true,
	)
}

func ClearRefreshCookie(c *gin.Context, cfg config.Config) {
	c.SetSameSite(parseSameSite(cfg.JWTCookieSameSite))
	c.SetCookie(
		cfg.JWTCookieName,
		"",
		-1,
		refreshCookiePath,
		"",
		cfg.JWTCookieSecure,
		true,
	)
}

func GetRefreshCookie(c *gin.Context, cfg config.Config) string {
	token, err := c.Cookie(cfg.JWTCookieName)
	if err != nil {
		return ""
	}
	return token
}

// GuestSessionCookie helpers manage the anonymous chat session identifier.
// HttpOnly prevents XSS-exfiltration; Secure follows app config; SameSite
// defaults to Lax to allow safe cross-origin redirect workflows.
func SetGuestSessionCookie(c *gin.Context, cfg config.Config, sessionID string, maxAgeSeconds int) {
	sameSite := parseSameSite(cfg.GuestCookieSameSite)
	c.SetSameSite(sameSite)
	secure := cfg.GuestCookieSecure
	if sameSite == http.SameSiteNoneMode {
		secure = true
	}
	c.SetCookie(
		guestSessionCookieName,
		sessionID,
		maxAgeSeconds,
		guestSessionCookiePath,
		"",
		secure,
		true,
	)
}

func GetGuestSessionCookie(c *gin.Context) string {
	sessionID, err := c.Cookie(guestSessionCookieName)
	if err != nil {
		return ""
	}
	return sessionID
}

func ClearGuestSessionCookie(c *gin.Context, cfg config.Config) {
	sameSite := parseSameSite(cfg.GuestCookieSameSite)
	c.SetSameSite(sameSite)
	secure := cfg.GuestCookieSecure || sameSite == http.SameSiteNoneMode
	c.SetCookie(
		guestSessionCookieName,
		"",
		-1,
		guestSessionCookiePath,
		"",
		secure,
		true,
	)
}

// SetGuestIdentityCookie stores the opaque guest bearer token. It intentionally
// uses /api/v1 (not the chat-only path) so the same identity protects chat,
// order creation, order tracking, and an explicit account-claim transition.
func SetGuestIdentityCookie(c *gin.Context, cfg config.Config, token string, maxAgeSeconds int) {
	sameSite := parseSameSite(cfg.GuestCookieSameSite)
	c.SetSameSite(sameSite)
	secure := cfg.GuestCookieSecure || sameSite == http.SameSiteNoneMode
	c.SetCookie(guestIdentityCookieName, token, maxAgeSeconds, guestIdentityCookiePath, "", secure, true)
}

func GetGuestIdentityCookie(c *gin.Context) string {
	token, err := c.Cookie(guestIdentityCookieName)
	if err != nil {
		return ""
	}
	return token
}

func ClearGuestIdentityCookie(c *gin.Context, cfg config.Config) {
	sameSite := parseSameSite(cfg.GuestCookieSameSite)
	c.SetSameSite(sameSite)
	secure := cfg.GuestCookieSecure || sameSite == http.SameSiteNoneMode
	c.SetCookie(guestIdentityCookieName, "", -1, guestIdentityCookiePath, "", secure, true)
}

func parseSameSite(value string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "lax":
		return http.SameSiteLaxMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteStrictMode
	}
}
