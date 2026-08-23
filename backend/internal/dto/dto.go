package dto

import "github.com/google/uuid"

type RegisterRequest struct {
	Name     string `json:"name" binding:"required,min=2"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

// AdminCreateUserRequest is used by the protected admin endpoint to provision
// staff (operator/admin) accounts. Unlike public registration, role here is
// honored because the caller is already authorized as admin.
type AdminCreateUserRequest struct {
	Name     string `json:"name" binding:"required,min=2"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	Role     string `json:"role" binding:"required,oneof=user operator admin"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password" binding:"required"`
}

// NOTE (SEC, 23 Agu 2026): there is intentionally NO RefreshRequest DTO. The
// refresh token travels ONLY in the HttpOnly cookie (Path=/api/v1/auth);
// accepting it in a JSON body would let JavaScript handle it and widen the
// XSS-exfiltration surface. Keep it that way.

type AuthResponse struct {
	AccessToken string `json:"access_token"`
	// RefreshToken is NEVER serialized: handlers clear it from the response
	// and deliver the token via SetRefreshCookie only. The field exists so a
	// populated value would be dropped by `json:"-"` even if a future code
	// path forgot to clear it (fail-closed).
	RefreshToken string      `json:"-"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    int64       `json:"expires_in"`
	User         interface{} `json:"user,omitempty"`
}

type ChatRequest struct {
	SessionID *uuid.UUID `json:"session_id"`
	Prompt    string     `json:"prompt" binding:"required,min=1,max=4000"`
	Stream    bool       `json:"stream"`
}

// ChatRecommendationReason describes why recommended packages are returned.
// Valid values: "initial", "alternative", or empty.
type ChatRecommendationReason string

const (
	ChatRecommendationReasonInitial     ChatRecommendationReason = "initial"
	ChatRecommendationReasonAlternative ChatRecommendationReason = "alternative"
)

type TripRequest struct {
	Title                string             `json:"title" binding:"required"`
	Slug                 string             `json:"slug"`
	Destination          string             `json:"destination"`
	Location             string             `json:"location"`
	Category             string             `json:"category"`
	Status               string             `json:"status"`
	Overview             string             `json:"overview"`
	Summary              string             `json:"summary"`
	Duration             string             `json:"duration"`
	AdultPax             int                `json:"adult_pax"`
	ChildPax             int                `json:"child_pax"`
	EstimatedPrice       float64            `json:"estimated_price" binding:"gte=0,lte=999999999999"`
	BasePrice            float64            `json:"base_price" binding:"gte=0,lte=999999999999"`
	DiscountPrice        float64            `json:"discount_price" binding:"gte=0,lte=999999999999"`
	ChildPrice           float64            `json:"child_price" binding:"gte=0,lte=999999999999"`
	ChildDiscountPrice   float64            `json:"child_discount_price" binding:"gte=0,lte=999999999999"`
	DiscountEnabled      bool               `json:"discount_enabled"`
	ChildDiscountEnabled bool               `json:"child_discount_enabled"`
	ImageURL             string             `json:"image_url"`
	Media                []TripMediaRequest `json:"media"`
	Highlights           []string           `json:"highlights"`
	AmenitiesIncluded    []string           `json:"amenities_included"`
	AmenitiesExcluded    []string           `json:"amenities_excluded"`
	References           []string           `json:"references"`
	ScheduleType         string             `json:"schedule_type"`
	PackageStartDate     string             `json:"package_start_date"`
	PackageEndDate       string             `json:"package_end_date"`
	PublishStartDate     string             `json:"publish_start_date"`
	PublishEndDate       string             `json:"publish_end_date"`
	Itineraries          []ItineraryRequest `json:"itineraries"`
}

type TripMediaRequest struct {
	URL     string `json:"url"`
	Type    string `json:"type"`
	AltText string `json:"alt_text"`
}

type ItineraryRequest struct {
	Day         int    `json:"day"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type TripListQuery struct {
	Category      string `form:"category"`
	Status        string `form:"status"`
	Search        string `form:"search"`
	PublishedOnly bool   `form:"published_only"`
	Limit         int    `form:"limit"`
	Offset        int    `form:"offset"`
}

// ListQuery is a generic pagination query for list endpoints (bookings, logs, etc.).
type ListQuery struct {
	Limit  int `form:"limit"`
	Offset int `form:"offset"`
}

// DefaultListLimit is the default page size when no limit is provided.
const DefaultListLimit = 50

// MaxListLimit is the maximum page size allowed to prevent excessive memory usage.
const MaxListLimit = 200

// Normalize clamps Limit and Offset to safe bounds and applies defaults.
func (q *ListQuery) Normalize() {
	if q.Limit <= 0 {
		q.Limit = DefaultListLimit
	}
	if q.Limit > MaxListLimit {
		q.Limit = MaxListLimit
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
}

type UploadResponse struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

// MaxBookingPax bounds adult/child pax per booking (SEC-11). Keep in sync with
// the binding tag on BookingRequest below.
const MaxBookingPax = 20

// BookingRequest no longer accepts a client-supplied price (SEC-3). The total
// is computed server-side from the trip catalog price and the requested pax.
type BookingRequest struct {
	TripID uuid.UUID `json:"trip_id" binding:"required"`
	// SEC-11: pax must stay within a sane range. Negative values would produce
	// negative/zero totals, and unbounded values risk float overflow/absurd
	// bills. The service layer re-enforces the same bounds for non-HTTP callers
	// (e.g. MCP create_booking) that bypass request binding.
	AdultPax     int    `json:"adult_pax" binding:"gte=0,lte=20"`
	ChildPax     int    `json:"child_pax" binding:"gte=0,lte=20"`
	ContactName  string `json:"contact_name"`
	ContactEmail string `json:"contact_email"`
	ContactPhone string `json:"contact_phone"`
	TravelDate   string `json:"travel_date"`
}

// PaymentCreateRequest no longer accepts a client-supplied amount (SEC-3). The
// amount is derived from the related booking's server-computed total.
type PaymentCreateRequest struct {
	BookingID     uuid.UUID `json:"booking_id" binding:"required"`
	PaymentMethod string    `json:"payment_method" binding:"required,oneof=QRIS VA VIRTUAL_ACCOUNT"`
}

// UpdateBookingStatusRequest is used by backoffice staff to advance a booking
// through the internal order workflow. Allowed transitions are enforced by the
// service layer, not by the client. Values are restricted to the canonical
// booking lifecycle constants (mirrors models.BookingStatus* — SEC-29).
type UpdateBookingStatusRequest struct {
	BookingStatus string `json:"booking_status" binding:"required,oneof=pending processing confirmed completed cancelled"`
}

// PaymentWebhookRequest is the DOKU payment callback payload. Status accepts
// any provider string; the service normalizes aliases into the canonical
// models.PaymentStatus* constants (SEC-29).
type PaymentWebhookRequest struct {
	ExternalID string   `json:"external_id" binding:"required"`
	Status     string   `json:"status" binding:"required"`
	Signature  string   `json:"signature"`
	Timestamp  string   `json:"timestamp"` // Received from header X-Doku-Timestamp
	RawBody    []byte   `json:"-"`         // Needed for true HMAC digest
	Amount     *float64 `json:"amount"`
}
