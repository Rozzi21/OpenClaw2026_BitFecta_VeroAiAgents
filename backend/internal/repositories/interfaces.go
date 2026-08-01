package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// SEC-27: Per-domain repository interfaces for Dependency Inversion.
// Services now depend on these narrow interfaces instead of the concrete
// *repositories.Repository, enabling unit-test mocking without a real database.
//
// The concrete *Repository type already satisfies every interface below
// implicitly (Go structural typing). No existing repository method signatures
// changed; only new aggregate methods were added for the analytics domain to
// fully retire the AnalyticsService `s.repo.DB` escape hatch (coding-rules
// §1.1a exception is now closed).
//
// Interface segregation: each service only sees the methods it actually calls.

// UserRepository — user CRUD.
type UserRepository interface {
	CreateUser(ctx context.Context, user *models.User) error
	FindUserByEmail(ctx context.Context, email string) (models.User, error)
	FirstOrCreateUser(ctx context.Context, user *models.User) error
	FindUserByID(ctx context.Context, id uuid.UUID) (models.User, error)
}

// AuthSessionRepository — JWT refresh session persistence.
type AuthSessionRepository interface {
	CreateAuthSession(ctx context.Context, userID uuid.UUID, tokenJTI string, expiresAt time.Time) error
	FindActiveSessionByJTI(ctx context.Context, tokenJTI string) (models.AuthSession, error)
	FindSessionByJTI(ctx context.Context, tokenJTI string) (models.AuthSession, error)
	RevokeSessionByJTI(ctx context.Context, tokenJTI string) error
	RotateSession(ctx context.Context, tokenJTI string) (rotated bool, err error)
	RevokeAllActiveSessionsByUser(ctx context.Context, userID uuid.UUID) error
	IsSessionRevoked(ctx context.Context, tokenJTI string) (bool, error)
	RevokeSessionByJTIIfExists(ctx context.Context, tokenJTI string) error
	CountActiveSessionsByJTI(ctx context.Context, tokenJTI string) (int64, error)
	RevokeSessionByJTIAllowMissing(ctx context.Context, tokenJTI string) error
}

// ChatRepository — chat session + message persistence.
type ChatRepository interface {
	CreateChatSession(ctx context.Context, session *models.ChatSession) error
	FindChatSession(ctx context.Context, id uuid.UUID) (models.ChatSession, error)
	UpdateChatSession(ctx context.Context, session *models.ChatSession) error
	UpdateChatSessionMemorySummary(ctx context.Context, sessionID uuid.UUID, summary string) error
	UpdateChatSessionSelectedTrip(ctx context.Context, sessionID uuid.UUID, tripID *uuid.UUID) error
	UpdateChatSessionActivity(ctx context.Context, sessionID uuid.UUID, expiresAt, lastActivityAt time.Time) error
	ListChatSessions(ctx context.Context, userID uuid.UUID) ([]models.ChatSession, error)
	DeleteExpiredChatSessions(ctx context.Context, before time.Time) (int64, error)
	CountExpiredChatSessions(ctx context.Context, before time.Time) (int64, error)
	AddChatMessage(ctx context.Context, message *models.ChatMessage) error
	ListChatMessages(ctx context.Context, sessionID uuid.UUID) ([]models.ChatMessage, error)
	ListRecentChatMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]models.ChatMessage, error)
	CountChatMessages(ctx context.Context, sessionID uuid.UUID) (int64, error)
	TailChatMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]models.ChatMessage, error)
}

// TripRepository — trip catalog + itinerary persistence.
type TripRepository interface {
	CreateTrip(ctx context.Context, trip *models.Trip) error
	ListTrips(ctx context.Context, query TripRepositoryFilter) ([]models.Trip, error)
	FindTrip(ctx context.Context, id uuid.UUID) (models.Trip, error)
	FindTripBySlugOrID(ctx context.Context, value string) (models.Trip, error)
	UpdateTrip(ctx context.Context, trip *models.Trip) error
	ReplaceTripItineraries(ctx context.Context, tripID uuid.UUID, itineraries []models.Itinerary) error
	DeleteTrip(ctx context.Context, id uuid.UUID) error
}

// BookingRepository — booking persistence + atomic status transitions.
type BookingRepository interface {
	FindBookingBySession(ctx context.Context, sessionID uuid.UUID) (models.Booking, error)
	CreateBooking(ctx context.Context, booking *models.Booking) error
	ListBookings(ctx context.Context, query RepositoryFilter) ([]models.Booking, error)
	RecentBookings(ctx context.Context, limit int) ([]models.Booking, error)
	FindBooking(ctx context.Context, id uuid.UUID) (models.Booking, error)
	FindBookingForUser(ctx context.Context, id, userID uuid.UUID) (models.Booking, error)
	UpdateBooking(ctx context.Context, booking *models.Booking) error
	UpdateBookingStatusAtomic(ctx context.Context, id uuid.UUID, fromStatus, toStatus string) (bool, error)
}

// PaymentRepository — payment persistence + atomic status transitions.
type PaymentRepository interface {
	CreatePayment(ctx context.Context, payment *models.Payment) error
	FindPayment(ctx context.Context, id uuid.UUID) (models.Payment, error)
	FindPaymentForUser(ctx context.Context, id, userID uuid.UUID) (models.Payment, error)
	FindPaymentByExternalID(ctx context.Context, externalID string) (models.Payment, error)
	UpdatePayment(ctx context.Context, payment *models.Payment) error
	UpdatePaymentStatusAtomic(ctx context.Context, id uuid.UUID, fromStatus, toStatus string) (bool, error)
}

// LogRepository — AI log + tool call audit persistence.
type LogRepository interface {
	CreateAILog(ctx context.Context, log *models.AILog) error
	ListAILogs(ctx context.Context, query RepositoryFilter) ([]models.AILog, error)
	CreateToolCall(ctx context.Context, call *models.ToolCall) error
	ListToolCalls(ctx context.Context, query RepositoryFilter) ([]models.ToolCall, error)
}

// AnalyticsRepository — aggregate queries for the dashboard. These dedicated
// methods retire the AnalyticsService `s.repo.DB` escape hatch (SEC-27 /
// coding-rules §1.1a): aggregate SQL now lives behind the repository layer
// where it belongs, and AnalyticsService depends only on this interface.
type AnalyticsRepository interface {
	RecentBookings(ctx context.Context, limit int) ([]models.Booking, error)
	CountBookings(ctx context.Context) (int64, error)
	SumBookingRevenue(ctx context.Context) (float64, error)
	CountTrips(ctx context.Context) (int64, error)
	CountAILogs(ctx context.Context) (int64, error)
	CountPayments(ctx context.Context) (int64, error)
	CountSuccessfulPayments(ctx context.Context) (int64, error)
}

// Ensure *Repository satisfies all domain interfaces at compile time.
var (
	_ UserRepository        = (*Repository)(nil)
	_ AuthSessionRepository = (*Repository)(nil)
	_ ChatRepository        = (*Repository)(nil)
	_ TripRepository        = (*Repository)(nil)
	_ BookingRepository     = (*Repository)(nil)
	_ PaymentRepository     = (*Repository)(nil)
	_ LogRepository         = (*Repository)(nil)
	_ AnalyticsRepository   = (*Repository)(nil)
)
