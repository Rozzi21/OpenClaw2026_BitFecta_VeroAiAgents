package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// defaultJWTSecret is the development fallback secret. It must never be used in
// production; Config.Validate enforces this.
const defaultJWTSecret = "super_secret_vero_travel"

// defaultDatabasePassword is a documented development placeholder. Production
// must provide a real database password so Docker/.env examples are never reused.
const defaultDatabasePassword = "change_me_dev_password"

type Config struct {
	AppEnv string
	Port   string

	DatabaseHost     string
	DatabasePort     string
	DatabaseUser     string
	DatabasePassword string
	DatabaseName     string
	DatabaseSSLMode  string
	DatabaseURL      string

	JWTSecret            string
	JWTAccessTTL         time.Duration
	JWTRefreshTTL        time.Duration
	JWTCookieName        string
	JWTCookieSecure      bool
	JWTCookieSameSite    string
	GuestCookieSecure    bool
	GuestCookieSameSite  string
	GuestSessionTTL      time.Duration
	GuestIdentityTTL     time.Duration
	AIAPIKey             string
	AIBaseURL            string
	AIModel              string
	AITemperature        float64
	AITimeout            time.Duration
	AIRecentMessages     int
	AIMemorySummaryAfter int
	AIMemoryMaxChars     int
	// PaymentsEnabled is a temporary feature flag for the DOKU payment flow.
	// Default false keeps booking/order creation manual-admin processed. Set
	// PAYMENTS_ENABLED=true to re-enable payment routes/webhooks/services later.
	PaymentsEnabled    bool
	DOKUClientID       string
	DOKUSecret         string
	N8NWebhook         string
	CORSAllowedOrigins []string

	// TrustedProxies is the list of reverse proxies that are trusted when
	// resolving the real client IP (X-Forwarded-For). Empty disables proxy
	// trust, which is the safest default for dev. Set to the real reverse proxy
	// CIDR(s) in production (SEC-14).
	TrustedProxies []string
}

func Load() Config {
	_ = godotenv.Load()

	accessMinutes := getInt("JWT_ACCESS_TTL_MINUTES", 15)
	refreshHours := getInt("JWT_REFRESH_TTL_HOURS", 720)
	aiTimeoutSeconds := getInt("AI_TIMEOUT_SECONDS", 35)

	cfg := Config{
		AppEnv:               getEnv("APP_ENV", "development"),
		Port:                 getEnv("PORT", "8080"),
		DatabaseHost:         getEnv("DATABASE_HOST", "localhost"),
		DatabasePort:         getEnv("DATABASE_PORT", "5432"),
		DatabaseUser:         getEnv("DATABASE_USER", "vero_user"),
		DatabasePassword:     getEnv("DATABASE_PASSWORD", ""),
		DatabaseName:         getEnv("DATABASE_NAME", "vero_travel"),
		DatabaseSSLMode:      getEnv("DATABASE_SSLMODE", "disable"),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		JWTSecret:            getEnv("JWT_SECRET", defaultJWTSecret),
		JWTAccessTTL:         time.Duration(accessMinutes) * time.Minute,
		JWTRefreshTTL:        time.Duration(refreshHours) * time.Hour,
		JWTCookieName:        getEnv("JWT_COOKIE_NAME", "refresh_token"),
		JWTCookieSecure:      getBoolEnv("JWT_COOKIE_SECURE", getEnv("APP_ENV", "development") == "production"),
		JWTCookieSameSite:    getEnv("JWT_COOKIE_SAME_SITE", "Strict"),
		GuestCookieSecure:    getBoolEnv("GUEST_COOKIE_SECURE", getEnv("APP_ENV", "development") == "production"),
		GuestCookieSameSite:  getEnv("GUEST_COOKIE_SAME_SITE", "Lax"),
		GuestSessionTTL:      time.Duration(getInt("GUEST_SESSION_TTL_HOURS", 168)) * time.Hour,
		GuestIdentityTTL:     time.Duration(getInt("GUEST_IDENTITY_TTL_HOURS", 720)) * time.Hour,
		AIAPIKey:             os.Getenv("AI_API_KEY"),
		AIBaseURL:            getEnv("AI_BASE_URL", "https://api.openai.com/v1"),
		AIModel:              getEnv("AI_MODEL", "gpt-4o-mini"),
		AITemperature:        getFloat("AI_TEMPERATURE", 0.4),
		AITimeout:            time.Duration(aiTimeoutSeconds) * time.Second,
		AIRecentMessages:     getInt("AI_CONTEXT_RECENT_MESSAGES", 8),
		AIMemorySummaryAfter: getInt("AI_MEMORY_SUMMARY_AFTER", 12),
		AIMemoryMaxChars:     getInt("AI_MEMORY_MAX_CHARS", 1800),
		PaymentsEnabled:      getBoolEnv("PAYMENTS_ENABLED", false),
		DOKUClientID:         os.Getenv("DOKU_CLIENT_ID"),
		DOKUSecret:           os.Getenv("DOKU_SECRET"),
		N8NWebhook:           os.Getenv("N8N_WEBHOOK"),
		CORSAllowedOrigins:   parseCSVEnv("CORS_ALLOWED_ORIGINS", []string{"http://localhost:3000", "http://localhost:3001", "http://localhost:5173"}),
		TrustedProxies:       parseCSVEnv("TRUSTED_PROXIES", nil),
	}

	if cfg.DatabaseURL == "" || strings.Contains(cfg.DatabaseURL, "YOUR_PASSWORD") {
		cfg.DatabaseURL = fmt.Sprintf(
			"host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
			cfg.DatabaseHost,
			cfg.DatabaseUser,
			cfg.DatabasePassword,
			cfg.DatabaseName,
			cfg.DatabasePort,
			cfg.DatabaseSSLMode,
		)
	}

	return cfg
}

// Validate enforces production-safety invariants. In production the JWT secret
// must be explicitly set to a non-default, non-empty value; otherwise tokens
// could be forged using the well-known development secret. When payments are
// explicitly re-enabled, the DOKU webhook secret is also required so payment
// webhooks cannot be forged (SEC-4).
func (c Config) Validate() error {
	if c.AppEnv == "production" {
		databaseURLProvided := strings.TrimSpace(c.DatabaseURL) != ""
		databaseURLUnsafe := strings.Contains(c.DatabaseURL, defaultDatabasePassword) || strings.Contains(c.DatabaseURL, "password_aman") || strings.Contains(c.DatabaseURL, "YOUR_PASSWORD")
		databasePasswordUnsafe := c.DatabasePassword == "" || c.DatabasePassword == defaultDatabasePassword
		if (databasePasswordUnsafe && !databaseURLProvided) || databaseURLUnsafe {
			return errors.New("DATABASE_PASSWORD must be set to a strong, non-default value when APP_ENV=production")
		}
		if c.JWTSecret == "" || c.JWTSecret == defaultJWTSecret {
			return errors.New("JWT_SECRET must be set to a strong, non-default value when APP_ENV=production")
		}
		if c.PaymentsEnabled && c.DOKUSecret == "" {
			return errors.New("DOKU_SECRET must be set when APP_ENV=production to verify payment webhooks")
		}
	}
	return nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getFloat(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

// parseCSVEnv reads a comma-separated env var into a trimmed slice, falling back
// to the provided default when the var is empty.
func parseCSVEnv(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return fallback
	}
	return result
}

func getBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
