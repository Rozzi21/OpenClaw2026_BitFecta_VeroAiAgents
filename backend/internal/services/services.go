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
	AI        *AIService
	MCP       *MCPService
	Trips     *TripService
	Bookings  *BookingService
	Payments  *PaymentService
	Logs      *LogService
	Analytics *AnalyticsService
}

func New(cfg config.Config, repo *repositories.Repository, jwt *auth.JWTService, bus *events.Bus) *Services {
	s := &Services{Config: cfg, Repo: repo, JWT: jwt, Events: bus}
	s.Auth = &AuthService{repo: repo, jwt: jwt, cfg: cfg}
	s.Bookings = &BookingService{repo: repo, bus: bus}
	s.MCP = &MCPService{repo: repo, bus: bus, bookings: s.Bookings, auth: s.Auth}
	aiClient := ai.NewClient(cfg.AIAPIKey, cfg.AIBaseURL, cfg.AIModel, cfg.AITemperature, cfg.AITimeout)
	s.AI = &AIService{repo: repo, mcp: s.MCP, bus: bus, client: aiClient, cfg: cfg}
	s.Trips = &TripService{repo: repo, bus: bus}
	s.Payments = &PaymentService{repo: repo, bus: bus, cfg: cfg}
	s.Logs = &LogService{repo: repo}
	s.Analytics = &AnalyticsService{repo: repo}
	return s
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
