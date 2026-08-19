package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Role string

const (
	RoleUser     Role = "user"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

// Booking lifecycle statuses (SEC-29). BookingStatus only advances through the
// transitions enforced by Booking.CanTransitionTo below.
const (
	BookingStatusPending    = "pending"
	BookingStatusProcessing = "processing"
	BookingStatusConfirmed  = "confirmed"
	BookingStatusCompleted  = "completed"
	BookingStatusCancelled  = "cancelled"
)

// Payment status constants (SEC-29). Values persisted in payments.status.
const (
	PaymentStatusPending    = "pending"
	PaymentStatusPaid       = "paid"
	PaymentStatusSettlement = "settlement"
	PaymentStatusVerified   = "verified"
	PaymentStatusFailed     = "failed"
	PaymentStatusExpired    = "expired"
	PaymentStatusCancelled  = "cancelled"
)

// paymentSuccessSet is the canonical set of statuses that mean "money
// received". DOKU sends "settlement" or "paid"; analytics historically also
// counted "verified". Centralized here so services do not re-declare raw
// string slices (SEC-29).
var paymentSuccessSet = map[string]bool{
	PaymentStatusPaid:       true,
	PaymentStatusSettlement: true,
	PaymentStatusVerified:   true,
}

// PaymentSuccessStatuses returns a copy of the success slice (for SQL IN).
func PaymentSuccessStatuses() []string {
	return []string{PaymentStatusPaid, PaymentStatusSettlement, PaymentStatusVerified}
}

// IsPaymentSuccess reports whether a (normalized) status means settled.
func IsPaymentSuccess(status string) bool {
	return paymentSuccessSet[status]
}

// NormalizePaymentStatus maps provider-specific aliases (DOKU, etc.) to the
// canonical lowercase status values above. Unknown values are returned
// trimmed+lowercased so the caller can decide how to treat them.
func NormalizePaymentStatus(status string) string {
	s := ""
	for _, r := range status {
		if r != ' ' && r != '-' && r != '_' {
			s += string(r)
		}
	}
	// fold case without importing strings here? simplest: manual lower
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	s = string(b)

	switch s {
	case "settlement":
		return PaymentStatusSettlement
	case "paid", "success", "capture", "authorized", "authorizedcapture":
		return PaymentStatusPaid
	case "pending", "waiting", "waitingpayment", "initiated", "new":
		return PaymentStatusPending
	case "failed", "failure", "denied", "deny", "declined":
		return PaymentStatusFailed
	case "expired":
		return PaymentStatusExpired
	case "cancelled", "canceled", "voided", "void":
		return PaymentStatusCancelled
	case "verified":
		return PaymentStatusVerified
	default:
		return s
	}
}

// Tool result status constants shared by MCP execution results (SEC-29).
// Previously raw literals "success"/"failed" were scattered across mcp_service.go.
const (
	ToolResultStatusSuccess = "success"
	ToolResultStatusFailed  = "failed"
)

// PaymentStatusWaitingPayment is the legacy initial payment_status used when
// the DOKU checkout flow is enabled. Kept for documentation; currently the
// chat flow uses PaymentStatusPendingAdminProcessing because payments are off.
const PaymentStatusWaitingPayment = "waiting_payment"

// PaymentStatusPendingAdminProcessing marks guest orders awaiting manual
// backoffice processing while the payment integration is disabled.
const PaymentStatusPendingAdminProcessing = "pending_admin_processing"

type BaseModel struct {
	ID        uuid.UUID      `json:"id" gorm:"type:uuid;primaryKey"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

func (m *BaseModel) BeforeCreate(_ *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	return nil
}

type User struct {
	BaseModel
	Name     string `json:"name" gorm:"size:120;not null"`
	Email    string `json:"email" gorm:"size:180;uniqueIndex;not null"`
	Password string `json:"-" gorm:"not null"`
	Role     Role   `json:"role" gorm:"size:30;not null;default:user"`
	// GoogleSub stores the immutable Google `sub` claim for accounts linked to
	// "Continue with Google". NULL for pure email/password accounts. Unique via
	// partial index (WHERE google_sub IS NOT NULL) so multiple NULLs are fine.
	GoogleSub    *string       `json:"-" gorm:"size:64;index"`
	ChatSessions []ChatSession `json:"-" gorm:"foreignKey:UserID"`
	Bookings     []Booking     `json:"-" gorm:"foreignKey:UserID"`
	AuthSessions []AuthSession `json:"-" gorm:"foreignKey:UserID"`

	// ExternalIdentities links this user to external OAuth/OIDC providers
	// (Google, etc.). The canonical "one Google account → one Vero account"
	// mapping lives there (identity keyed by `sub`, NOT by email alone).
	ExternalIdentities []ExternalIdentity `json:"-" gorm:"foreignKey:UserID"`
}

// ExternalIdentity maps a Vero user to an external identity provider account.
// Identity is keyed by the provider's immutable `ProviderUserID` (Google `sub`)
// — NOT by email, which is mutable and only a hint for first-time linking.
//
// The UNIQUE(provider, provider_user_id) composite index guarantees one
// provider account resolves to exactly one Vero user. Email is stored for
// display/audit only and is not a uniqueness key.
type ExternalIdentity struct {
	BaseModel
	// UserID is the Vero account this external identity resolves to.
	UserID uuid.UUID `json:"user_id" gorm:"type:uuid;not null;index"`
	// Provider is the OAuth/OIDC provider slug, e.g. "google". Part of the
	// composite unique key so multiple providers can coexist per user.
	Provider string `json:"provider" gorm:"size:30;not null;uniqueIndex:idx_ext_ident_provider_user"`
	// ProviderUserID is the provider's immutable subject (Google `sub`).
	// Part of the composite unique key — the canonical identity key.
	ProviderUserID string `json:"provider_user_id" gorm:"size:128;not null;uniqueIndex:idx_ext_ident_provider_user"`
	// Email is the provider-reported email at link time. Informational only;
	// NOT used for uniqueness or login resolution (sub is the key).
	Email string `json:"email" gorm:"size:180"`
	// Picture is the provider profile photo URL (Google `picture`). Optional
	// provider metadata — never part of the identity key.
	Picture string `json:"picture" gorm:"type:text"`

	User User `json:"-" gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// ExternalIdentityProviderGoogle is the canonical provider slug for Google.
const ExternalIdentityProviderGoogle = "google"

// OAuthState is a one-time, short-lived record backing the OAuth 2.0 `state`
// parameter for Google login. The raw state is never stored — StateHash holds
// its SHA-256 digest — and the row is consumed atomically at callback time
// (same atomic-UPDATE pattern as AuthSession rotation, BUG-1) so a state can
// never be replayed. Nonce binds the Google id_token to this flow.
type OAuthState struct {
	BaseModel
	StateHash  string     `json:"-" gorm:"size:64;uniqueIndex;not null"`
	Nonce      string     `json:"-" gorm:"size:64;not null"`
	ReturnTo   string     `json:"-" gorm:"size:255;not null;default:/"`
	ExpiresAt  time.Time  `json:"-" gorm:"index;not null"`
	ConsumedAt *time.Time `json:"-" gorm:"index"`
	// LinkUserID is set ONLY for the explicit "Link Google Account" flow (an
	// authenticated user linking their Google identity). NULL for the normal
	// login flow. When set, the callback links the verified Google sub to THIS
	// user instead of resolving/creating an account — this is what makes
	// account linking require proof of Vero-account ownership (no email merge).
	LinkUserID *uuid.UUID `json:"-" gorm:"type:uuid;index"`
}

type AuthSession struct {
	BaseModel
	UserID    uuid.UUID  `json:"user_id" gorm:"type:uuid;index;not null"`
	User      User       `json:"-" gorm:"foreignKey:UserID"`
	TokenJTI  string     `json:"token_jti" gorm:"size:64;uniqueIndex;not null"`
	ExpiresAt time.Time  `json:"expires_at" gorm:"index;not null"`
	RevokedAt *time.Time `json:"revoked_at,omitempty" gorm:"index"`
}

// GuestSession is the durable, server-side identity for an unauthenticated
// visitor. The browser only receives an opaque random token; TokenHash stores
// its SHA-256 digest so a database leak cannot be used as a bearer credential.
type GuestSession struct {
	BaseModel
	TokenHash    string     `json:"-" gorm:"size:64;uniqueIndex;not null"`
	UserID       uuid.UUID  `json:"-" gorm:"type:uuid;index;not null"`
	FirstOrderID *uuid.UUID `json:"first_order_id,omitempty" gorm:"type:uuid;index"`
	OrderCount   int        `json:"order_count" gorm:"not null;default:0"`
	ExpiresAt    time.Time  `json:"expires_at" gorm:"index;not null"`
}

type ChatSession struct {
	BaseModel
	Title          string        `json:"title" gorm:"size:180;not null"`
	MemorySummary  string        `json:"memory_summary" gorm:"type:text"`
	SelectedTripID *uuid.UUID    `json:"selected_trip_id" gorm:"type:uuid;index"`
	Messages       []ChatMessage `json:"messages,omitempty" gorm:"foreignKey:SessionID"`
	UserID         *uuid.UUID    `json:"user_id" gorm:"type:uuid;index"`
	GuestSessionID *uuid.UUID    `json:"-" gorm:"type:uuid;index"`
	User           *User         `json:"-" gorm:"foreignKey:UserID"`
	ExpiresAt      *time.Time    `json:"expires_at" gorm:"index"`
	LastActivityAt *time.Time    `json:"last_activity_at" gorm:"index"`
}

type ChatMessage struct {
	BaseModel
	SessionID uuid.UUID   `json:"session_id" gorm:"type:uuid;index;not null"`
	Session   ChatSession `json:"-" gorm:"foreignKey:SessionID"`
	Role      string      `json:"role" gorm:"size:30;not null"`
	Content   string      `json:"content" gorm:"type:text;not null"`
}

type Trip struct {
	BaseModel
	Title                string      `json:"title" gorm:"size:180;not null"`
	Slug                 string      `json:"slug" gorm:"size:220;uniqueIndex;not null"`
	Destination          string      `json:"destination" gorm:"size:180;not null;index"`
	Location             string      `json:"location" gorm:"size:180;index"`
	Category             string      `json:"category" gorm:"size:40;not null;default:international;index"`
	Status               string      `json:"status" gorm:"size:40;not null;default:draft;index"`
	Overview             string      `json:"overview" gorm:"type:text"`
	Summary              string      `json:"summary" gorm:"type:text"`
	Duration             string      `json:"duration" gorm:"size:80"`
	AdultPax             int         `json:"adult_pax" gorm:"not null;default:0"`
	ChildPax             int         `json:"child_pax" gorm:"not null;default:0"`
	EstimatedPrice       float64     `json:"estimated_price" gorm:"type:numeric(14,2);not null;default:0"`
	BasePrice            float64     `json:"base_price" gorm:"type:numeric(14,2);not null;default:0"`
	DiscountPrice        float64     `json:"discount_price" gorm:"type:numeric(14,2);not null;default:0"`
	ChildPrice           float64     `json:"child_price" gorm:"type:numeric(14,2);not null;default:0"`
	ChildDiscount        float64     `json:"child_discount_price" gorm:"type:numeric(14,2);not null;default:0"`
	DiscountEnabled      bool        `json:"discount_enabled" gorm:"not null;default:false"`
	ChildDiscountEnabled bool        `json:"child_discount_enabled" gorm:"not null;default:false"`
	ImageURL             string      `json:"image_url" gorm:"type:text"`
	Media                []TripMedia `json:"media" gorm:"serializer:json;type:jsonb"`
	Highlights           []string    `json:"highlights" gorm:"serializer:json;type:jsonb"`
	AmenitiesIncluded    []string    `json:"amenities_included" gorm:"serializer:json;type:jsonb"`
	AmenitiesExcluded    []string    `json:"amenities_excluded" gorm:"serializer:json;type:jsonb"`
	References           []string    `json:"references" gorm:"serializer:json;type:jsonb"`
	ScheduleType         string      `json:"schedule_type" gorm:"size:60"`
	PackageStartDate     *time.Time  `json:"package_start_date,omitempty"`
	PackageEndDate       *time.Time  `json:"package_end_date,omitempty"`
	PublishStartDate     *time.Time  `json:"publish_start_date,omitempty"`
	PublishEndDate       *time.Time  `json:"publish_end_date,omitempty"`
	PublishedAt          *time.Time  `json:"published_at,omitempty"`
	Itineraries          []Itinerary `json:"itineraries,omitempty" gorm:"foreignKey:TripID;constraint:OnDelete:CASCADE"`
}

type TripMedia struct {
	URL     string `json:"url"`
	Type    string `json:"type"`
	AltText string `json:"alt_text,omitempty"`
}

type Itinerary struct {
	BaseModel
	TripID      uuid.UUID `json:"trip_id" gorm:"type:uuid;index;not null"`
	Trip        Trip      `json:"-" gorm:"foreignKey:TripID"`
	Day         int       `json:"day" gorm:"not null"`
	Title       string    `json:"title" gorm:"size:180;not null"`
	Description string    `json:"description" gorm:"type:text"`
}

type Booking struct {
	BaseModel
	UserID             uuid.UUID  `json:"user_id" gorm:"type:uuid;index;not null"`
	User               User       `json:"user,omitempty" gorm:"foreignKey:UserID"`
	GuestSessionID     *uuid.UUID `json:"-" gorm:"type:uuid;index"`
	TripID             uuid.UUID  `json:"trip_id" gorm:"type:uuid;index;not null"`
	Trip               Trip       `json:"trip,omitempty" gorm:"foreignKey:TripID"`
	BookingStatus      string     `json:"booking_status" gorm:"size:40;not null;default:pending;index"`
	PaymentStatus      string     `json:"payment_status" gorm:"size:40;not null;default:waiting_payment;index"`
	AdultPax           int        `json:"adult_pax" gorm:"not null;default:1"`
	ChildPax           int        `json:"child_pax" gorm:"not null;default:0"`
	ContactName        string     `json:"contact_name" gorm:"size:120"`
	ContactEmail       string     `json:"contact_email" gorm:"size:180"`
	ContactPhone       string     `json:"contact_phone" gorm:"size:40"`
	TravelDate         *time.Time `json:"travel_date,omitempty"`
	TotalPrice         float64    `json:"total_price" gorm:"type:numeric(14,2);not null"`
	BookingDate        time.Time  `json:"booking_date" gorm:"not null"`
	Payments           []Payment  `json:"payments,omitempty" gorm:"foreignKey:BookingID"`
	IdempotencyKeyHash string     `json:"-" gorm:"size:64;uniqueIndex"`
}

// CanTransitionTo reports whether the booking may move to target under the
// SEC-29 status machine. Terminal states (completed / cancelled) accept no
// further transitions; identical source and target are a no-op.
func (b *Booking) CanTransitionTo(target string) bool {
	if b.BookingStatus == target {
		return true
	}
	allowed, ok := bookingStatusTransitions[b.BookingStatus]
	if !ok {
		return false
	}
	return allowed[target]
}

// bookingStatusTransitions centralizes the booking lifecycle graph so both
// validation and any future reporting read the same truth (SEC-29).
var bookingStatusTransitions = map[string]map[string]bool{
	BookingStatusPending: {
		BookingStatusProcessing: true,
		BookingStatusConfirmed:  true,
		BookingStatusCancelled:  true,
	},
	BookingStatusProcessing: {
		BookingStatusConfirmed: true,
		BookingStatusCancelled: true,
	},
	BookingStatusConfirmed: {
		BookingStatusCompleted: true,
		BookingStatusCancelled: true,
	},
	BookingStatusCompleted: {},
	BookingStatusCancelled: {},
}

type Payment struct {
	BaseModel
	BookingID     uuid.UUID `json:"booking_id" gorm:"type:uuid;index;not null"`
	Booking       Booking   `json:"-" gorm:"foreignKey:BookingID"`
	PaymentMethod string    `json:"payment_method" gorm:"size:50;not null"`
	ExternalID    string    `json:"external_id" gorm:"size:160;index"`
	Amount        float64   `json:"amount" gorm:"type:numeric(14,2);not null"`
	Status        string    `json:"status" gorm:"size:40;not null;default:pending"`
	ExpiredAt     time.Time `json:"expired_at"`
}

type AILog struct {
	BaseModel
	SessionID     *uuid.UUID `json:"session_id,omitempty" gorm:"type:uuid;index"`
	Workflow      string     `json:"workflow" gorm:"size:160;not null"`
	ToolName      string     `json:"tool_name" gorm:"size:120"`
	Status        string     `json:"status" gorm:"size:40;not null"`
	ExecutionTime int64      `json:"execution_time" gorm:"not null;default:0"`
	Response      string     `json:"response" gorm:"type:jsonb;default:'{}'"`
}

type ToolCall struct {
	BaseModel
	SessionID uuid.UUID `json:"session_id" gorm:"type:uuid;index;not null"`
	ToolName  string    `json:"tool_name" gorm:"size:120;not null"`
	Payload   string    `json:"payload" gorm:"type:jsonb;default:'{}'"`
	Result    string    `json:"result" gorm:"type:jsonb;default:'{}'"`
	Status    string    `json:"status" gorm:"size:40;not null"`
}
