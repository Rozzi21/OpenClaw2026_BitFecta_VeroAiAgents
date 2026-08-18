package middlewares

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
	"golang.org/x/time/rate"
)

const (
	ContextUserID = "user_id"
	ContextRole   = "role"
	ContextEmail  = "email"
)

// CORS builds the CORS middleware from the configured allow-list (SEC-8). Origins
// come from CORS_ALLOWED_ORIGINS so production domains can be added without code
// changes.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	origins := allowedOrigins
	if len(origins) == 0 {
		origins = []string{"http://localhost:3000", "http://localhost:3001", "http://localhost:5173"}
	}
	return cors.New(cors.Config{
		AllowOrigins:     origins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Authorization", "Content-Type", "X-Request-ID", "X-Doku-Signature"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
}

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Header("X-Request-ID", requestID)
		c.Set("request_id", requestID)
		ctx := context.WithValue(c.Request.Context(), "request_id", requestID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func SecureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "0")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

// Recovery logs panic detail (including request id) to the server log but never
// leaks it to the client (SEC-6 information disclosure).
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		requestID, _ := c.Get("request_id")
		log.Printf("panic recovered request_id=%v path=%s: %v", requestID, c.FullPath(), recovered)
		utils.Error(c, http.StatusInternalServerError, "Unexpected server error", gin.H{})
	})
}

// maxRateLimiterEntries caps the in-memory map so a botnet rotating IPs cannot
// exhaust server memory (SEC-14). 10k entries ≈ a few MB of limiters, and is
// far more than legitimate concurrent-IP demand for a single instance.
const maxRateLimiterEntries = 10_000

// ipRateLimiter keeps one token-bucket limiter per client IP so a single client
// cannot exhaust the quota for everyone (SEC-7).
type ipRateLimiter struct {
	limiters   sync.Map // map[string]*rate.Limiter
	every      rate.Limit
	burst      int
	entries    atomic.Int64
	maxEntries int64
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastUsed atomic.Int64
}

func newIPRateLimiter(every rate.Limit, burst int) *ipRateLimiter {
	l := &ipRateLimiter{every: every, burst: burst, maxEntries: maxRateLimiterEntries}
	// Janitor: evict idle limiters and enforce a hard cap on map size (SEC-14).
	go l.janitor()
	return l
}

func (l *ipRateLimiter) get(ip string) *rate.Limiter {
	if existing, ok := l.limiters.Load(ip); ok {
		entry := existing.(*rateLimiterEntry)
		entry.lastUsed.Store(time.Now().UnixNano())
		return entry.limiter
	}
	// Reserve capacity atomically. The old count() implementation scanned the
	// entire map for every new IP, allowing rotating-IP traffic to amplify CPU
	// work to O(n²) as the map approached its cap.
	if !l.reserveEntry() {
		return rate.NewLimiter(l.every, l.burst)
	}
	now := time.Now().UnixNano()
	entry := &rateLimiterEntry{limiter: rate.NewLimiter(l.every, l.burst)}
	entry.lastUsed.Store(now)
	actual, loaded := l.limiters.LoadOrStore(ip, entry)
	if loaded {
		l.entries.Add(-1)
		entry = actual.(*rateLimiterEntry)
		entry.lastUsed.Store(now)
	}
	return entry.limiter
}

func (l *ipRateLimiter) reserveEntry() bool {
	for {
		current := l.entries.Load()
		if current >= l.maxEntries {
			return false
		}
		if l.entries.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

// janitor periodically removes limiters that have been idle long enough that
// their tokens have refilled to burst (SEC-14). This keeps memory bounded even
// under a high-rotation IP attack.
func (l *ipRateLimiter) janitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		l.evictIdle(now, time.Minute)
	}
}

func (l *ipRateLimiter) evictIdle(now time.Time, idleFor time.Duration) {
	cutoff := now.Add(-idleFor).UnixNano()
	l.limiters.Range(func(key, value interface{}) bool {
		entry := value.(*rateLimiterEntry)
		if entry.lastUsed.Load() >= cutoff {
			return true
		}
		// CompareAndDelete avoids deleting a replacement inserted for the same
		// IP between Range and deletion.
		if l.limiters.CompareAndDelete(key, entry) {
			l.entries.Add(-1)
		}
		return true
	})
}

func (l *ipRateLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.get(c.ClientIP()).Allow() {
			utils.Error(c, http.StatusTooManyRequests, "Too many requests", gin.H{})
			c.Abort()
			return
		}
		c.Next()
	}
}

// RateLimit applies a per-IP global limit (20 req/s, burst 20).
func RateLimit() gin.HandlerFunc {
	return newIPRateLimiter(rate.Every(time.Second), 20).middleware()
}

// AuthRateLimit applies a stricter per-IP limit for auth endpoints to slow down
// credential brute-force (5 req/s, burst 5).
func AuthRateLimit() gin.HandlerFunc {
	return newIPRateLimiter(rate.Every(time.Second), 5).middleware()
}

// PublicWriteRateLimit throttles expensive unauthenticated write endpoints
// (POST /chat, POST /orders) to 5 req/min per-IP (SEC-13). The global 20 req/s
// limit was enough to spam thousands of fake bookings / LLM-cost-heavy chats;
// a per-minute budget keeps normal usage working while making bulk abuse
// impractical.
func PublicWriteRateLimit() gin.HandlerFunc {
	return newIPRateLimiter(rate.Every(12*time.Second), 5).middleware()
}

// RequestBodyLimit caps request bodies on JSON endpoints exposed to guests
// (SEC-16). Upload routes keep their separate multipart limit in main.go.
func RequestBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

func Auth(jwtService *auth.JWTService) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			utils.Unauthorized(c, "Missing bearer token")
			c.Abort()
			return
		}

		token := strings.TrimPrefix(header, "Bearer ")
		claims, err := jwtService.ParseWithAudience(token, auth.AudienceAccess)
		if err != nil {
			if parsedClaims, parseErr := jwtService.Parse(token); parseErr == nil && auth.IsAudience(parsedClaims, auth.AudienceRefresh) {
				requestID, _ := c.Get("request_id")
				id, _ := requestID.(string)
				auth.LogSecurity(auth.EventRefreshTokenUsedAsAccess, map[string]any{
					"user_id":    parsedClaims.UserID.String(),
					"email":      parsedClaims.Email,
					"jti":        parsedClaims.ID,
					"ip":         c.ClientIP(),
					"user_agent": c.GetHeader("User-Agent"),
					"request_id": id,
				})
			}
			utils.Unauthorized(c, "Invalid or expired token")
			c.Abort()
			return
		}

		c.Set(ContextUserID, claims.UserID)
		c.Set(ContextRole, claims.Role)
		c.Set(ContextEmail, claims.Email)
		c.Next()
	}
}

func Role(allowed ...models.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, exists := c.Get(ContextRole)
		if !exists {
			utils.Forbidden(c, "Role required")
			c.Abort()
			return
		}
		role, ok := value.(models.Role)
		if !ok {
			utils.Forbidden(c, "Invalid role")
			c.Abort()
			return
		}
		for _, item := range allowed {
			if role == item {
				c.Next()
				return
			}
		}
		utils.Forbidden(c, "Insufficient permission")
		c.Abort()
	}
}
