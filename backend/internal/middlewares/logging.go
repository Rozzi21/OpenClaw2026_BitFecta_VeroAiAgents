package middlewares

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// StructuredLogger returns a Gin middleware that logs HTTP requests using slog.
func StructuredLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

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
