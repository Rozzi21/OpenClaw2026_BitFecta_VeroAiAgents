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
	Name         string        `json:"name" gorm:"size:120;not null"`
	Email        string        `json:"email" gorm:"size:180;uniqueIndex;not null"`
	Password     string        `json:"-" gorm:"not null"`
	Role         Role          `json:"role" gorm:"size:30;not null;default:user"`
	ChatSessions []ChatSession `json:"-" gorm:"foreignKey:UserID"`
	Bookings     []Booking     `json:"-" gorm:"foreignKey:UserID"`
	AuthSessions []AuthSession `json:"-" gorm:"foreignKey:UserID"`
}

type AuthSession struct {
	BaseModel
	UserID    uuid.UUID  `json:"user_id" gorm:"type:uuid;index;not null"`
	User      User       `json:"-" gorm:"foreignKey:UserID"`
	TokenJTI  string     `json:"token_jti" gorm:"size:64;uniqueIndex;not null"`
	ExpiresAt time.Time  `json:"expires_at" gorm:"index;not null"`
	RevokedAt *time.Time `json:"revoked_at,omitempty" gorm:"index"`
}

type ChatSession struct {
	BaseModel
	Title          string        `json:"title" gorm:"size:180;not null"`
	MemorySummary  string        `json:"memory_summary" gorm:"type:text"`
	SelectedTripID *uuid.UUID    `json:"selected_trip_id" gorm:"type:uuid;index"`
	Messages       []ChatMessage `json:"messages,omitempty" gorm:"foreignKey:SessionID"`
	UserID         *uuid.UUID    `json:"user_id" gorm:"type:uuid;index"`
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
	UserID        uuid.UUID  `json:"user_id" gorm:"type:uuid;index;not null"`
	User          User       `json:"user,omitempty" gorm:"foreignKey:UserID"`
	TripID        uuid.UUID  `json:"trip_id" gorm:"type:uuid;index;not null"`
	Trip          Trip       `json:"trip,omitempty" gorm:"foreignKey:TripID"`
	BookingStatus string     `json:"booking_status" gorm:"size:40;not null;default:pending"`
	PaymentStatus string     `json:"payment_status" gorm:"size:40;not null;default:waiting_payment"`
	AdultPax      int        `json:"adult_pax" gorm:"not null;default:1"`
	ChildPax      int        `json:"child_pax" gorm:"not null;default:0"`
	ContactName   string     `json:"contact_name" gorm:"size:120"`
	ContactEmail  string     `json:"contact_email" gorm:"size:180"`
	ContactPhone  string     `json:"contact_phone" gorm:"size:40"`
	TravelDate    *time.Time `json:"travel_date,omitempty"`
	TotalPrice    float64    `json:"total_price" gorm:"type:numeric(14,2);not null"`
	BookingDate   time.Time  `json:"booking_date" gorm:"not null"`
	Payments      []Payment  `json:"payments,omitempty" gorm:"foreignKey:BookingID"`
}

// CanTransitionTo checks if booking status transition is valid
func (b *Booking) CanTransitionTo(target string) bool {
	if b.BookingStatus == target {
		return true
	}
	transitions := map[string]map[string]bool{
		"pending":    {"processing": true, "confirmed": true, "cancelled": true},
		"processing": {"confirmed": true, "cancelled": true},
		"confirmed":  {"completed": true, "cancelled": true},
		"completed":  {},
		"cancelled":  {},
	}
	allowed, ok := transitions[b.BookingStatus]
	if !ok {
		return false
	}
	return allowed[target]
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
