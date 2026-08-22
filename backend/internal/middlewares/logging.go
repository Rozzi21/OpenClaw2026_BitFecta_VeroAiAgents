package middlewares

import (
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// StructuredLogger returns a Gin middleware that logs HTTP requests using slog.
func StructuredLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		// SEC-hardening (23 Agu 2026): never log raw OAuth/query secrets. The
		// Google callback arrives as ?code=<authorization_code>&state=<state>;
		// logging RawQuery verbatim would persist a single-use auth code and
		// anti-CSRF state into logs. Redact sensitive keys before logging.
		query := redactSensitiveQuery(c.Request.URL.RawQuery)

		c.Next()

		// Skip logging metrics and health checks to prevent log bloating in production
		if path == "/metrics" || path == "/healthz" || path == "/readyz" {
			return
		}

		end := time.Now()
		latency := end.Sub(start)

		requestID, _ := c.Get("request_id")
		reqIDStr, _ := requestID.(string)

		status := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		userAgent := c.Request.UserAgent()
		errorMessage := c.Errors.ByType(gin.ErrorTypePrivate).String()

		attributes := []any{
			slog.String("request_id", reqIDStr),
			slog.String("method", method),
			slog.String("path", path),
			slog.String("query", query),
			slog.Int("status", status),
			slog.String("ip", clientIP),
			slog.Duration("latency", latency),
			slog.String("user_agent", userAgent),
		}

		if errorMessage != "" {
			attributes = append(attributes, slog.String("error", errorMessage))
		}

		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}

		slog.Log(c.Request.Context(), level, "http_request", attributes...)
	}
}

// sensitiveQueryKeys are query parameter names that must never be written to
// logs because they carry credentials or single-use OAuth artifacts. Matched
// case-insensitively after url-decoding the key.
var sensitiveQueryKeys = map[string]struct{}{
	"code":          {}, // OAuth authorization code (single-use, but sensitive)
	"state":         {}, // OAuth anti-CSRF state
	"access_token":  {}, // bearer access token (defense-in-depth)
	"refresh_token": {}, // refresh token (defense-in-depth)
	"id_token":      {}, // OIDC id_token
	"token":         {}, // generic bearer token
	"client_secret": {}, // OAuth client secret
	"password":      {}, // credential (defense-in-depth)
}

// redactSensitiveQuery parses rawQuery and re-encodes it with the values of
// sensitive keys replaced by "[redacted]". It preserves non-sensitive keys and
// ordering so logs stay useful for debugging. On any parse failure it returns
// the empty string rather than risk leaking a secret (fail-closed).
func redactSensitiveQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	for key := range values {
		if _, sensitive := sensitiveQueryKeys[strings.ToLower(key)]; sensitive {
			values.Set(key, "[redacted]")
		}
	}
	return values.Encode()
}
