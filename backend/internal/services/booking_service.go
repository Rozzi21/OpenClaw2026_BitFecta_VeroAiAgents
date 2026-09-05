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

// CodeGuestOrderLimitReached is the STABLE machine-readable code every transport
// exposes when ErrGuestOrderLimitReached escapes the booking domain: HTTP
// (`error.code` on 403, booking_handlers.go), MCP tool results (`data.code`,
// mcp_service.go) and the chat order gate (ChatResult.OrderGate, ai_service.go).
//
// It is declared here, next to the error it maps from, because it is the ONLY
// thing clients and the LLM are allowed to branch on. The human-readable
// message is for display and may be reworded/translated at any time; the code
// may not. Never derive the guest rule from a message string — the rule lives in
// BookingService.create and is enforced by the database.
const CodeGuestOrderLimitReached = "GUEST_ORDER_LIMIT_REACHED"

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
	// FindBookingByIdempotency is also used OUTSIDE the transaction, to replay
	// the winner's booking after a lost insert race on the same key (GO-P2-3).
	FindBookingByIdempotency(ctx context.Context, ownerID uuid.UUID, guest bool, hash string) (models.Booking, error)
}

func hashIdempotency(ownerID uuid.UUID, guest bool, key string) string {
	prefix := "user:"
	if guest {
		prefix = "guest:"
	}
	sum := sha256.Sum256([]byte(prefix + ownerID.String() + ":" + key))
	return hex.EncodeToString(sum[:])
}

// maxClaimedGuestIdempotencyScopes bounds how many previously claimed guest
// identities the authenticated path re-checks for a replayed Idempotency-Key
// (GO-P2-4). A guest identity can hold at most ONE order, so this is also the
// number of guest orders an account may have absorbed; 5 covers a customer who
// ordered as a guest from several browsers before signing in, while keeping the
// work per booking request constant.
const maxClaimedGuestIdempotencyScopes = 5

// claimedGuestIdempotencyLookup is the read side needed to recognise an
// Idempotency-Key that was first used before the caller signed in. Satisfied by
// both the transaction handle and the plain repository.
type claimedGuestIdempotencyLookup interface {
	FindBookingByIdempotency(ctx context.Context, ownerID uuid.UUID, guest bool, hash string) (models.Booking, error)
	ListClaimedGuestSessionIDs(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error)
}

// findClaimedGuestIdempotencyReplay resolves the booking an Idempotency-Key
// already produced while the caller was still a guest (GO-P2-4).
//
// bookings.idempotency_key_hash binds the key to its OWNER
// (sha256("guest:"+guestSessionID+":"+key) vs sha256("user:"+userID+":"+key)),
// which is what keeps two different owners using the same key apart. The claim
// changes the owner without being able to rehash anything (the raw key is never
// stored), so the account replaying the very request it made as a guest used to
// miss its own order and create a SECOND one — precisely at the moment a client
// retries the request that was refused with GUEST_ORDER_LIMIT_REACHED.
//
// The lookup re-derives the guest-scoped hash from the claim marker and keeps the
// owner filter on the caller: `user_id = caller AND guest_session_id IS NULL AND
// idempotency_key_hash = <guest hash>`. Both halves are needed to hit a row, and
// both are the caller's own — the guest session ids come from claims made BY this
// account, so another owner's order can never be returned, and nothing is
// weakened for callers that never were a guest (no marker ⇒ no lookup).
func findClaimedGuestIdempotencyReplay(ctx context.Context, repo claimedGuestIdempotencyLookup, userID uuid.UUID, key string) (models.Booking, bool) {
	if userID == uuid.Nil {
		return models.Booking{}, false
	}
	guestIDs, err := repo.ListClaimedGuestSessionIDs(ctx, userID, maxClaimedGuestIdempotencyScopes)
	if err != nil || len(guestIDs) == 0 {
		return models.Booking{}, false
	}
	for _, guestID := range guestIDs {
		existing, err := repo.FindBookingByIdempotency(ctx, userID, false, hashIdempotency(guestID, true, key))
		if err == nil {
			return existing, true
		}
	}
	return models.Booking{}, false
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
	// GO-P0-1: contact anchors are the cookie-independent half of the guest
	// entitlement. Derived once here, then checked AND consumed inside the same
	// transaction below so the database stays the sole authority.
	anchors := guestContactAnchors(req)
	limitReason := guestLimitReasonSessionSpent
	matchedGuestSessionID := ""
	var booking models.Booking
	err := s.repo.WithBookingTransaction(ctx, func(tx repositories.BookingTransactionRepository) error {
		if existing, err := tx.FindBookingByIdempotency(ctx, ownerID, isGuest, keyHash); err == nil {
			booking = existing
			return nil
		}
		if !isGuest {
			// GO-P2-4: the same logical request may already have been placed by
			// this very customer while they were still a guest, with the key
			// hashed under the guest scope. Replay that order instead of
			// creating a second one for the same key.
			if existing, ok := findClaimedGuestIdempotencyReplay(ctx, tx, userID, idempotencyKey); ok {
				booking = existing
				return nil
			}
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
			// A guest order must carry at least one anchorable contact, otherwise
			// the entitlement would fall back to depending solely on the
			// discardable cookie. An unusable contact ("abc" as a phone, a string
			// without "@" as an email) is a validation failure — it consumes
			// nothing, exactly like the other checks below.
			if len(anchors) == 0 {
				return ErrBookingContactRequired
			}
			// Same contact, different guest identity: the visitor cleared the
			// cookie / opened a private window / called the API without a cookie
			// jar after already spending the single guest order.
			if used, err := tx.FindGuestOrderEntitlement(ctx, guestContactKeys(anchors)); err == nil {
				limitReason = guestLimitReasonContactSpent
				if used.GuestSessionID != nil {
					matchedGuestSessionID = used.GuestSessionID.String()
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
			// GO-P0-1: consume the contact anchors in the same transaction. The
			// unique index on guest_order_entitlements.contact_key decides the
			// winner when two requests race past the read above, so concurrent
			// guests sharing a contact cannot both persist an order.
			if err := tx.ConsumeGuestOrderEntitlements(ctx, guestOrderEntitlements(anchors, *guestID, booking.ID)); err != nil {
				limitReason = guestLimitReasonContactSpent
				return ErrGuestOrderLimitReached
			}
		}
		return nil
	})
	if err != nil {
		// Lost insert race on the SAME Idempotency-Key (GO-P2-3). Two requests
		// with identical owner + key can both pass the pre-insert lookup above;
		// bookings.idempotency_key_hash (UNIQUE) then rejects the loser and
		// aborts its transaction, so the loser used to surface a raw constraint
		// error (HTTP 500) even though the winner had already persisted the very
		// booking this key stands for. Re-read with the SAME owner-scoped lookup
		// and replay it.
		//
		// No check is weakened: the lookup is keyed by the caller's own owner id
		// (guest session id for guests, user id otherwise) plus the key hash, so
		// it can only ever return a booking this caller created with this key —
		// never another owner's order, and never a second order for a guest. The
		// loser's transaction (booking insert + entitlement consumption) was
		// rolled back, so no allowance was spent twice.
		if replay, replayErr := s.repo.FindBookingByIdempotency(ctx, ownerID, isGuest, keyHash); replayErr == nil {
			return replay, nil
		}
		if errors.Is(err, ErrGuestOrderLimitReached) {
			payload := map[string]any{"guest_session_id": ownerID.String(), "reason": limitReason}
			if matchedGuestSessionID != "" {
				payload["matched_guest_session_id"] = matchedGuestSessionID
			}
			auth.LogSecurity("guest_order_limit_reached", payload)
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
