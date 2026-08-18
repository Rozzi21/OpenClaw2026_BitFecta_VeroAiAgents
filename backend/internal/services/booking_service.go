package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

// ErrBookingNotFound is a sentinel error for missing bookings.
var ErrBookingNotFound = errors.New("booking not found")
var ErrGuestOrderLimitReached = errors.New("guest order limit reached")
var ErrIdempotencyKeyRequired = errors.New("idempotency key is required")
var ErrBookingContactRequired = errors.New("contact email or phone is required")
var ErrBookingTravelDateInvalid = errors.New("travel date is invalid")

// SEC-27: BookingService depends on a narrow repository interface instead of
// the concrete *repositories.Repository. It needs bookings + trip lookup
// (Create resolves the trip for server-side pricing, SEC-3).
type BookingService struct {
	repo BookingRepository
	bus  *events.Bus
}

// BookingRepository is the narrow repository contract BookingService uses
// (SEC-27): booking persistence + the trip catalog read needed to price the
// booking server-side. Composed from domain interfaces in
// repositories/interfaces.go.
type BookingRepository interface {
	repositories.BookingRepository
	FindTrip(ctx context.Context, id uuid.UUID) (models.Trip, error)
	WithBookingTransaction(ctx context.Context, fn func(repositories.BookingTransactionRepository) error) error
	FindBookingForGuest(ctx context.Context, id, guestID uuid.UUID) (models.Booking, error)
}

func hashIdempotency(ownerID uuid.UUID, guest bool, key string) string {
	prefix := "user:"
	if guest {
		prefix = "guest:"
	}
	sum := sha256.Sum256([]byte(prefix + ownerID.String() + ":" + key))
	return hex.EncodeToString(sum[:])
}

func (s *BookingService) Create(ctx context.Context, userID uuid.UUID, idempotencyKey string, req dto.BookingRequest) (models.Booking, error) {
	return s.create(ctx, userID, nil, idempotencyKey, req)
}

func (s *BookingService) CreateGuest(ctx context.Context, userID, guestID uuid.UUID, idempotencyKey string, req dto.BookingRequest) (models.Booking, error) {
	return s.create(ctx, userID, &guestID, idempotencyKey, req)
}

func (s *BookingService) create(ctx context.Context, userID uuid.UUID, guestID *uuid.UUID, idempotencyKey string, req dto.BookingRequest) (models.Booking, error) {
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 200 {
		return models.Booking{}, ErrIdempotencyKeyRequired
	}
	ownerID := userID
	isGuest := guestID != nil
	if isGuest {
		ownerID = *guestID
	}
	keyHash := hashIdempotency(ownerID, isGuest, idempotencyKey)
	var booking models.Booking
	err := s.repo.WithBookingTransaction(ctx, func(tx repositories.BookingTransactionRepository) error {
		if existing, err := tx.FindBookingByIdempotency(ctx, ownerID, isGuest, keyHash); err == nil {
			booking = existing
			return nil
		}
		if isGuest {
			guest, err := tx.LockGuestSession(ctx, *guestID)
			if err != nil {
				return ErrGuestSessionInvalid
			}
			if guest.OrderCount >= 1 {
				if existing, err := tx.FindBookingByIdempotency(ctx, ownerID, true, keyHash); err == nil {
					booking = existing
					return nil
				}
				return ErrGuestOrderLimitReached
			}
		}

		// SEC-3: never trust a client-supplied price. Resolve the trip and compute
		// the total from the catalog price and the requested pax server-side.
		trip, err := tx.FindTrip(ctx, req.TripID)
		if err != nil {
			return errors.New("trip not found")
		}
		// SEC-11: enforce sane pax bounds server-side too, not only via DTO binding,
		// because non-HTTP callers (MCP create_booking) bypass request binding.
		// Negative pax would yield negative/zero totals; huge pax risks float
		// overflow and absurd bills.
		adultPax := req.AdultPax
		childPax := req.ChildPax
		if adultPax < 0 || childPax < 0 || adultPax > dto.MaxBookingPax || childPax > dto.MaxBookingPax {
			return fmt.Errorf("pax must be between 0 and %d", dto.MaxBookingPax)
		}
		if adultPax <= 0 && childPax <= 0 {
			adultPax = 1
		}
		if req.ContactEmail == "" && req.ContactPhone == "" {
			return ErrBookingContactRequired
		}
		travelDate := parseDate(req.TravelDate)
		if travelDate == nil {
			return ErrBookingTravelDateInvalid
		}
		if trip.Status != "published" {
			return errors.New("trip is unavailable")
		}
		if trip.PackageStartDate != nil && travelDate.Before(*trip.PackageStartDate) {
			return errors.New("trip is unavailable for travel date")
		}
		if trip.PackageEndDate != nil && travelDate.After(*trip.PackageEndDate) {
			return errors.New("trip is unavailable for travel date")
		}
		if trip.AdultPax > 0 && adultPax > trip.AdultPax || trip.ChildPax > 0 && childPax > trip.ChildPax {
			return errors.New("trip capacity exceeded")
		}
		// Reuse the shared priceBreakdown helper so the booking total is computed by
		// the exact same code path that backs the calculate_trip_price tool. This
		// keeps a quoted total identical to the charged total (source of truth).
		total := priceBreakdown(trip, adultPax, childPax).Total
		booking = models.Booking{
			UserID:         userID,
			GuestSessionID: guestID,
			TripID:         req.TripID,
			BookingStatus:  models.BookingStatusPending,
			// Payments are temporarily disabled. New orders stay pending for manual
			// backoffice/admin processing. Re-enable DOKU by restoring the old
			// waiting_payment status alongside PAYMENTS_ENABLED=true.
			PaymentStatus: models.PaymentStatusPendingAdminProcessing,

			AdultPax:           adultPax,
			ChildPax:           childPax,
			ContactName:        req.ContactName,
			ContactEmail:       req.ContactEmail,
			ContactPhone:       req.ContactPhone,
			TravelDate:         travelDate,
			TotalPrice:         total,
			BookingDate:        time.Now(),
			IdempotencyKeyHash: keyHash,
		}
		if err := tx.CreateBooking(ctx, &booking); err != nil {
			return err
		}
		if isGuest {
			if err := tx.ConsumeGuestOrder(ctx, *guestID, booking.ID); err != nil {
				return ErrGuestOrderLimitReached
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrGuestOrderLimitReached) {
			auth.LogSecurity("guest_order_limit_reached", map[string]any{"guest_session_id": ownerID.String()})
		}
		return models.Booking{}, err
	}
	// SEC-18: minimal signal only; the booking struct carries contact PII
	// (name/email/phone) that must not be broadcast to every SSE subscriber.
	s.bus.Publish("booking_created", map[string]interface{}{"booking_id": booking.ID, "trip_id": booking.TripID, "status": booking.BookingStatus})
	if isGuest {
		auth.LogSecurity("guest_order_created", map[string]any{"guest_session_id": ownerID.String(), "booking_id": booking.ID.String()})
	}
	return booking, nil
}
func (s *BookingService) List(ctx context.Context, query dto.ListQuery) ([]models.Booking, error) {
	repoQuery := repositories.RepositoryFilter{
		Limit:  query.Limit,
		Offset: query.Offset,
	}
	return s.repo.ListBookings(ctx, repoQuery)
}

// Find enforces ownership for non-staff callers (SEC-2 anti-IDOR).
func (s *BookingService) Find(ctx context.Context, id, userID uuid.UUID, isStaff bool) (models.Booking, error) {
	var booking models.Booking
	var err error
	if isStaff {
		booking, err = s.repo.FindBooking(ctx, id)
	} else {
		booking, err = s.repo.FindBookingForUser(ctx, id, userID)
	}
	if err != nil {
		return models.Booking{}, ErrBookingNotFound
	}
	return booking, nil
}

func (s *BookingService) FindGuest(ctx context.Context, id, guestID uuid.UUID) (models.Booking, error) {
	booking, err := s.repo.FindBookingForGuest(ctx, id, guestID)
	if err != nil {
		return models.Booking{}, ErrBookingNotFound
	}
	return booking, nil
}

// UpdateStatus allows backoffice staff to advance a booking through the
// internal workflow. It enforces allowed transitions server-side and returns
// the updated booking.
//
// SEC-23 fix: uses an atomic conditional UPDATE (WHERE booking_status = current)
// instead of read-validate-write, eliminating the TOCTOU race where two concurrent
// requests could both pass validation and write conflicting transitions.
func (s *BookingService) UpdateStatus(ctx context.Context, id, userID uuid.UUID, isStaff bool, req dto.UpdateBookingStatusRequest) (models.Booking, error) {
	booking, err := s.Find(ctx, id, userID, isStaff)
	if err != nil {
		return models.Booking{}, err
	}

	current := booking.BookingStatus
	target := req.BookingStatus

	if current == target {
		return booking, nil
	}

	if !booking.CanTransitionTo(target) {
		return models.Booking{}, fmt.Errorf("invalid status transition from %s to %s", current, target)
	}

	// Atomic conditional update: only succeeds if the DB status still matches
	// what we read. If another request changed it first, RowsAffected == 0.
	updated, err := s.repo.UpdateBookingStatusAtomic(ctx, id, current, target)
	if err != nil {
		return models.Booking{}, err
	}
	if !updated {
		// Race lost: another request changed the status between our read and write.
		// Re-fetch to report the actual current state to the caller.
		fresh, fetchErr := s.Find(ctx, id, userID, isStaff)
		if fetchErr != nil {
			return models.Booking{}, fmt.Errorf("concurrent status change detected for booking %s", id)
		}
		return models.Booking{}, fmt.Errorf("concurrent status change detected: booking is now %q (expected %q)", fresh.BookingStatus, current)
	}

	// Re-fetch so the caller receives the latest persisted state with preloads.
	fetchedBooking, err := s.Find(ctx, id, userID, isStaff)
	if err != nil {
		return models.Booking{}, err
	}
	s.bus.Publish("booking_updated", map[string]interface{}{"booking_id": fetchedBooking.ID, "status": fetchedBooking.BookingStatus})
	return fetchedBooking, nil
}

// tripAdultPrice/tripChildPrice resolve the effective price honoring discounts.
func tripAdultPrice(trip models.Trip) float64 {
	if trip.DiscountEnabled && trip.DiscountPrice > 0 {
		return trip.DiscountPrice
	}
	return firstNonZero(trip.BasePrice, trip.EstimatedPrice)
}

func tripChildPrice(trip models.Trip) float64 {
	if trip.ChildDiscountEnabled && trip.ChildDiscount > 0 {
		return trip.ChildDiscount
	}
	return trip.ChildPrice
}

// TripPriceBreakdown is the authoritative, AI-facing price breakdown for a
// trip. It reuses tripAdultPrice/tripChildPrice — the SAME logic BookingService
// .Create uses to compute TotalPrice — so a quote from calculate_trip_price is
// guaranteed identical to the total charged when the booking is created. The
// LLM must never compute totals itself; it reads Total from this struct.
type TripPriceBreakdown struct {
	// Normal (pre-discount) catalog unit prices.
	AdultNormalPrice float64 `json:"adult_normal_price"`
	ChildNormalPrice float64 `json:"child_normal_price"`
	// Effective (post-discount, when enabled) unit prices actually charged.
	AdultUnitPrice float64 `json:"adult_unit_price"`
	ChildUnitPrice float64 `json:"child_unit_price"`
	// Discount flags + amounts (0/absent when the discount is off).
	AdultDiscountEnabled bool    `json:"adult_discount_enabled"`
	AdultDiscountPrice   float64 `json:"adult_discount_price,omitempty"`
	ChildDiscountEnabled bool    `json:"child_discount_enabled"`
	ChildDiscountPrice   float64 `json:"child_discount_price,omitempty"`
	// Quantities used for the quote.
	AdultPax int `json:"adult_pax"`
	ChildPax int `json:"child_pax"`
	// Line subtotals + final total (source of truth == booking total).
	AdultSubtotal float64 `json:"adult_subtotal"`
	ChildSubtotal float64 `json:"child_subtotal"`
	Total         float64 `json:"total"`
}

// priceBreakdown builds the authoritative quote for a trip + pax counts. Shared
// by the MCP calculate_trip_price tool and kept in lockstep with Create above.
func priceBreakdown(trip models.Trip, adultPax, childPax int) TripPriceBreakdown {
	adultUnit := tripAdultPrice(trip)
	childUnit := tripChildPrice(trip)
	adultSub := adultUnit * float64(adultPax)
	childSub := childUnit * float64(childPax)
	return TripPriceBreakdown{
		AdultNormalPrice:     firstNonZero(trip.BasePrice, trip.EstimatedPrice),
		ChildNormalPrice:     trip.ChildPrice,
		AdultUnitPrice:       adultUnit,
		ChildUnitPrice:       childUnit,
		AdultDiscountEnabled: trip.DiscountEnabled && trip.DiscountPrice > 0,
		AdultDiscountPrice:   trip.DiscountPrice,
		ChildDiscountEnabled: trip.ChildDiscountEnabled && trip.ChildDiscount > 0,
		ChildDiscountPrice:   trip.ChildDiscount,
		AdultPax:             adultPax,
		ChildPax:             childPax,
		AdultSubtotal:        adultSub,
		ChildSubtotal:        childSub,
		Total:                adultSub + childSub,
	}
}
