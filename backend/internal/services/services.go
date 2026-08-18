package services

import (
	"errors"

	"github.com/google/uuid"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/ai"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

// ChatContext carries the session boundary resolved by the HTTP layer. A nil
// UserID represents an anonymous guest session; authenticated callers can be
// added later without changing the AI service contract again.
type ChatContext struct {
	SessionID uuid.UUID
	UserID    *uuid.UUID
}

// Services wires the per-domain service structs together. Each domain lives in
// its own file within this package (auth_service.go, ai_service.go,
// mcp_service.go, trip_service.go, booking_service.go, payment_service.go,
// log_service.go, analytics_service.go), with shared helpers in helpers.go.
type Services struct {
	Config    config.Config
	Repo      *repositories.Repository
	JWT       *auth.JWTService
	Events    *events.Bus
	Auth      *AuthService
	Google    *GoogleOAuthService
	Guests    *GuestService
	AI        *AIService
	MCP       *MCPService
	Trips     *TripService
	Bookings  *BookingService
	Payments  *PaymentService
	Logs      *LogService
	Analytics *AnalyticsService
	// audit persists MCP tool-call + AI-log records asynchronously (PERF-3 #2).
	// Call Stop during graceful shutdown so in-flight audit records are flushed.
	audit *AuditPool
}

func New(cfg config.Config, repo *repositories.Repository, jwt *auth.JWTService, bus *events.Bus) *Services {
	s := &Services{Config: cfg, Repo: repo, JWT: jwt, Events: bus}
	s.Auth = &AuthService{repo: repo, jwt: jwt, cfg: cfg}
	s.Google = NewGoogleOAuthService(cfg, repo, s.Auth)
	s.Guests = &GuestService{repo: repo, cfg: cfg, users: s.Auth}
	s.Bookings = &BookingService{repo: repo, bus: bus}
	// PERF-3 #2: bounded audit worker pool detaches tool-call + AI-log
	// persistence from the synchronous LLM response path. *Repository satisfies
	// AuditWriter implicitly (SEC-27 structural typing).
	s.audit = NewAuditPool(repo)
	s.audit.Start()
	s.MCP = &MCPService{repo: repo, bus: bus, bookings: s.Bookings, auth: s.Auth, audit: s.audit}
	aiClient := ai.NewClient(cfg.AIAPIKey, cfg.AIBaseURL, cfg.AIModel, cfg.AITemperature, cfg.AITimeout)
	s.AI = &AIService{repo: repo, mcp: s.MCP, bus: bus, client: aiClient, cfg: cfg}
	s.Trips = &TripService{repo: repo, bus: bus}
	s.Payments = &PaymentService{repo: repo, bus: bus, cfg: cfg}
	s.Logs = &LogService{repo: repo}
	s.Analytics = &AnalyticsService{repo: repo}
	return s
}

// StopAudit drains the MCP audit worker pool (PERF-3 #2). Call during graceful
// shutdown so in-flight tool-call + AI-log records are persisted before the
// process exits. Safe to call multiple times.
func (s *Services) StopAudit() {
	if s.audit != nil {
		s.audit.Stop()
	}
}

// AuthRequestMeta carries request-scoped audit context (IP, UA, request id)
// shared by all AuthService operations.
type AuthRequestMeta struct {
	IP        string
	UserAgent string
	RequestID string
}

// AuthIssueResult is the result of issuing a new session (access token response
// plus the refresh token/JTI to be set as an HttpOnly cookie).
type AuthIssueResult struct {
	Response     dto.AuthResponse
	RefreshToken string
	RefreshJTI   string
}

var (
	ErrRefreshTokenRevoked = errors.New("refresh token revoked")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	ErrPaymentsDisabled    = errors.New("payment feature temporarily disabled")
	ErrChatSessionNotFound = errors.New("chat session not found")
	ErrChatSessionExpired  = errors.New("chat session expired")
)
