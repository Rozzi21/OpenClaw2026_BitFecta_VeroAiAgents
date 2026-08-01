package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

// ErrBookingNotFound is a sentinel error for missing bookings.
var ErrBookingNotFound = errors.New("booking not found")

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
}

func (s *BookingService) Create(ctx context.Context, userID uuid.UUID, req dto.BookingRequest) (models.Booking, error) {
	// SEC-3: never trust a client-supplied price. Resolve the trip and compute
	// the total from the catalog price and the requested pax server-side.
	trip, err := s.repo.FindTrip(ctx, req.TripID)
	if err != nil {
		return models.Booking{}, errors.New("trip not found")
	}
	// SEC-11: enforce sane pax bounds server-side too, not only via DTO binding,
	// because non-HTTP callers (MCP create_booking) bypass request binding.
	// Negative pax would yield negative/zero totals; huge pax risks float
	// overflow and absurd bills.
	adultPax := req.AdultPax
	childPax := req.ChildPax
	if adultPax < 0 || childPax < 0 || adultPax > dto.MaxBookingPax || childPax > dto.MaxBookingPax {
		return models.Booking{}, fmt.Errorf("pax must be between 0 and %d", dto.MaxBookingPax)
	}
	if adultPax <= 0 && childPax <= 0 {
		adultPax = 1
	}
	total := tripAdultPrice(trip)*float64(adultPax) + tripChildPrice(trip)*float64(childPax)
	booking := models.Booking{
		UserID:        userID,
		TripID:        req.TripID,
		BookingStatus: models.BookingStatusPending,
		// Payments are temporarily disabled. New orders stay pending for manual
		// backoffice/admin processing. Re-enable DOKU by restoring the old
		// waiting_payment status alongside PAYMENTS_ENABLED=true.
		PaymentStatus: models.PaymentStatusPendingAdminProcessing,

		AdultPax:     adultPax,
		ChildPax:     childPax,
		ContactName:  req.ContactName,
		ContactEmail: req.ContactEmail,
		ContactPhone: req.ContactPhone,
		TravelDate:   parseDate(req.TravelDate),
		TotalPrice:   total,
		BookingDate:  time.Now(),
	}
	if err := s.repo.CreateBooking(ctx, &booking); err != nil {
		return booking, err
	}
	// SEC-18: minimal signal only; the booking struct carries contact PII
	// (name/email/phone) that must not be broadcast to every SSE subscriber.
	s.bus.Publish("booking_created", map[string]interface{}{"booking_id": booking.ID, "trip_id": booking.TripID, "status": booking.BookingStatus})
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
