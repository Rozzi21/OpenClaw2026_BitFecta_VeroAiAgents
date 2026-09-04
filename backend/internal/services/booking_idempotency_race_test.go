package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"gorm.io/gorm"
)

// Deterministic test for the lost insert race on a shared Idempotency-Key
// (GO-P2-3). On Postgres the loser's transaction is ABORTED by
// bookings.idempotency_key_hash (UNIQUE), so the winner's row can only be read
// after that transaction ends — which is why the replay lookup lives outside it
// and cannot be exercised by the SQLite harness (single connection, serialized
// transactions, the in-transaction lookup always wins).
//
// The stub reproduces exactly that shape: the transaction fails, and the winner's
// booking is visible afterwards.

type raceBookingRepo struct {
	txErr   error
	winner  models.Booking
	hasRow  bool
	lastKey struct {
		ownerID uuid.UUID
		guest   bool
		hash    string
	}
	lookups int
}

func (r *raceBookingRepo) WithBookingTransaction(_ context.Context, _ func(repositories.BookingTransactionRepository) error) error {
	return r.txErr
}

func (r *raceBookingRepo) FindBookingByIdempotency(_ context.Context, ownerID uuid.UUID, guest bool, hash string) (models.Booking, error) {
	r.lookups++
	r.lastKey.ownerID, r.lastKey.guest, r.lastKey.hash = ownerID, guest, hash
	if !r.hasRow {
		return models.Booking{}, gorm.ErrRecordNotFound
	}
	return r.winner, nil
}

// Unused by the paths under test; present to satisfy services.BookingRepository.
func (r *raceBookingRepo) FindTrip(context.Context, uuid.UUID) (models.Trip, error) {
	return models.Trip{}, gorm.ErrRecordNotFound
}
func (r *raceBookingRepo) FindBookingForGuest(context.Context, uuid.UUID, uuid.UUID) (models.Booking, error) {
	return models.Booking{}, gorm.ErrRecordNotFound
}
func (r *raceBookingRepo) FindBookingBySession(context.Context, uuid.UUID) (models.Booking, error) {
	return models.Booking{}, gorm.ErrRecordNotFound
}
func (r *raceBookingRepo) CreateBooking(context.Context, *models.Booking) error { return nil }
func (r *raceBookingRepo) ListBookings(context.Context, repositories.RepositoryFilter) ([]models.Booking, error) {
	return nil, nil
}
func (r *raceBookingRepo) RecentBookings(context.Context, int) ([]models.Booking, error) {
	return nil, nil
}
func (r *raceBookingRepo) FindBooking(context.Context, uuid.UUID) (models.Booking, error) {
	return models.Booking{}, gorm.ErrRecordNotFound
}
func (r *raceBookingRepo) FindBookingForUser(context.Context, uuid.UUID, uuid.UUID) (models.Booking, error) {
	return models.Booking{}, gorm.ErrRecordNotFound
}
func (r *raceBookingRepo) UpdateBooking(context.Context, *models.Booking) error { return nil }
func (r *raceBookingRepo) UpdateBookingStatusAtomic(context.Context, uuid.UUID, string, string) (bool, error) {
	return false, nil
}

func raceBookingReq() dto.BookingRequest {
	return dto.BookingRequest{TripID: uuid.New(), AdultPax: 1, ContactName: "Guest",
		ContactEmail: "race@example.com", TravelDate: "2030-01-01"}
}

// TestGuestOrderLostIdempotencyRaceReplaysWinner: the loser of the insert race
// returns the winner's booking instead of a constraint error, and the lookup it
// used is scoped to the CALLER's own guest session + key hash — so the replay can
// never reach another owner's order.
func TestGuestOrderLostIdempotencyRaceReplaysWinner(t *testing.T) {
	guestID := uuid.New()
	guestUserID := uuid.New()
	const key = "race-idem-000000000001"

	winner := models.Booking{UserID: guestUserID, GuestSessionID: &guestID}
	winner.ID = uuid.New()
	repo := &raceBookingRepo{
		txErr:  errors.New(`duplicate key value violates unique constraint "idx_bookings_idempotency_key_hash"`),
		winner: winner,
		hasRow: true,
	}
	svc := &BookingService{repo: repo, bus: events.NewBus()}

	booking, err := svc.CreateGuest(context.Background(), guestUserID, guestID, key, raceBookingReq())
	if err != nil {
		t.Fatalf("lost race must replay the winner, got err %v", err)
	}
	if booking.ID != winner.ID {
		t.Fatalf("replayed the wrong booking: %s want %s", booking.ID, winner.ID)
	}
	if repo.lookups != 1 {
		t.Fatalf("expected exactly one replay lookup, got %d", repo.lookups)
	}
	if repo.lastKey.ownerID != guestID || !repo.lastKey.guest {
		t.Fatalf("replay lookup not scoped to the caller's guest session: owner=%s guest=%v",
			repo.lastKey.ownerID, repo.lastKey.guest)
	}
	if want := hashIdempotency(guestID, true, key); repo.lastKey.hash != want {
		t.Fatalf("replay lookup used a different key hash: %s want %s", repo.lastKey.hash, want)
	}
}

// TestGuestOrderFailureWithoutWinnerPropagates: with no booking under that key
// the original error must survive — the replay net may not turn a real failure
// (or a legitimate guest-limit refusal) into a success.
func TestGuestOrderFailureWithoutWinnerPropagates(t *testing.T) {
	guestID := uuid.New()
	guestUserID := uuid.New()

	for name, txErr := range map[string]error{
		"limit_reached":  ErrGuestOrderLimitReached,
		"contact_needed": ErrBookingContactRequired,
		"infrastructure": errors.New("connection refused"),
	} {
		repo := &raceBookingRepo{txErr: txErr, hasRow: false}
		svc := &BookingService{repo: repo, bus: events.NewBus()}

		booking, err := svc.CreateGuest(context.Background(), guestUserID, guestID, "race-idem-000000000002", raceBookingReq())
		if !errors.Is(err, txErr) {
			t.Fatalf("%s: expected the original error, got booking=%s err=%v", name, booking.ID, err)
		}
		if booking.ID != uuid.Nil {
			t.Fatalf("%s: no booking may be returned, got %s", name, booking.ID)
		}
		if repo.lookups != 1 {
			t.Fatalf("%s: expected one replay lookup, got %d", name, repo.lookups)
		}
	}
}

// TestAuthenticatedOrderLostIdempotencyRaceReplaysWinner: same net on the
// authenticated path, scoped by user id instead of guest session id. Behaviour of
// a normal (non-racing) authenticated booking is unchanged — only the loser of an
// identical-key race stops seeing a 500.
func TestAuthenticatedOrderLostIdempotencyRaceReplaysWinner(t *testing.T) {
	userID := uuid.New()
	const key = "race-idem-000000000003"

	winner := models.Booking{UserID: userID}
	winner.ID = uuid.New()
	repo := &raceBookingRepo{txErr: errors.New("duplicate key"), winner: winner, hasRow: true}
	svc := &BookingService{repo: repo, bus: events.NewBus()}

	booking, err := svc.Create(context.Background(), userID, key, raceBookingReq())
	if err != nil {
		t.Fatalf("lost race must replay the winner, got err %v", err)
	}
	if booking.ID != winner.ID {
		t.Fatalf("replayed the wrong booking: %s want %s", booking.ID, winner.ID)
	}
	if repo.lastKey.ownerID != userID || repo.lastKey.guest {
		t.Fatalf("replay lookup not scoped to the caller's account: owner=%s guest=%v",
			repo.lastKey.ownerID, repo.lastKey.guest)
	}
	if want := hashIdempotency(userID, false, key); repo.lastKey.hash != want {
		t.Fatalf("replay lookup used a different key hash: %s want %s", repo.lastKey.hash, want)
	}
}
