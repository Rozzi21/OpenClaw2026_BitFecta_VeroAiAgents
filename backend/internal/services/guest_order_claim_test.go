package services_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"gorm.io/gorm"
)

// Secure guest-order claiming after authentication (GO-P1-3 / GO-P3-3).
//
// The flow under test: a visitor orders as a guest, authenticates (password or
// Google — the claim path is identical for both), and the pending guest order
// becomes property of that account. What these tests pin down is everything the
// transition must REFUSE:
//
//   - a guest cannot claim another guest's order (the cookie resolves one
//     session, and the booking must still reference that exact session);
//   - an authenticated user cannot claim a guest order without that cookie;
//   - knowing the order id proves nothing (the API takes no order id at all);
//   - owning the same email as the order contact proves nothing;
//   - an already-claimed order is never transferred to a second account, and
//     never silently — the refusal is an explicit sentinel;
//   - a repeated claim by the rightful account is a success that changes
//     nothing (idempotent), so retries are safe;
//   - concurrent claims transfer ownership exactly once;
//   - the transfer plus its claim marker are one transaction.
//
// Harness note (GO-P3-6): SQLite in-memory with MaxOpenConns(1) serializes the
// concurrency test through a single connection, so `SELECT ... FOR UPDATE` is
// not exercised as it would be on Postgres. What IS exercised is the logic that
// holds on Postgres regardless of locking: the marker read before any write plus
// the two conditional UPDATEs.

func newGuestClaimServices(repo *repositories.Repository) *services.Services {
	return services.New(config.Config{}, repo, nil, events.NewBus())
}

// seedClaimGuestSession creates an extra guest identity (its own isolated guest
// user, like production's guest-<uuid>@vero.local) resolvable by the raw token.
func seedClaimGuestSession(t *testing.T, db *gorm.DB, token string) (models.User, models.GuestSession) {
	t.Helper()
	user := models.User{Name: "Guest Traveler", Email: "guest-" + uuid.NewString() + "@vero.local", Password: "x", Role: models.RoleUser}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create guest user: %v", err)
	}
	guest := models.GuestSession{TokenHash: services.HashGuestToken(token), UserID: user.ID, ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := db.Create(&guest).Error; err != nil {
		t.Fatalf("create guest session: %v", err)
	}
	return user, guest
}

// seedClaimAccount creates the authenticated account a claim targets. The email
// is explicit so the email-only attack test can mint an account that shares the
// order's contact address.
func seedClaimAccount(t *testing.T, db *gorm.DB, email string) models.User {
	t.Helper()
	account := models.User{Name: "Account", Email: email, Password: "x", Role: models.RoleUser}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	return account
}

// claimBookingReq keeps every guest order in a test on distinct contact anchors;
// reusing one phone/email would trip the unrelated one-order-per-contact rule
// (GO-P0-1) instead of the claim logic under test.
func claimBookingReq(trip models.Trip, seq int) dto.BookingRequest {
	req := validBookingReq(trip)
	req.ContactPhone = fmt.Sprintf("081200%05d", seq)
	req.ContactEmail = fmt.Sprintf("guest%05d@example.com", seq)
	return req
}

func reloadBooking(t *testing.T, db *gorm.DB, id uuid.UUID) models.Booking {
	t.Helper()
	var booking models.Booking
	if err := db.First(&booking, "id = ?", id).Error; err != nil {
		t.Fatalf("reload booking: %v", err)
	}
	return booking
}

func reloadGuestSession(t *testing.T, db *gorm.DB, id uuid.UUID) models.GuestSession {
	t.Helper()
	var guest models.GuestSession
	if err := db.First(&guest, "id = ?", id).Error; err != nil {
		t.Fatalf("reload guest session: %v", err)
	}
	return guest
}

// assertStillGuestOwned fails when a refused claim moved anything.
func assertStillGuestOwned(t *testing.T, db *gorm.DB, bookingID, guestID, guestUserID uuid.UUID) {
	t.Helper()
	booking := reloadBooking(t, db, bookingID)
	if booking.GuestSessionID == nil || *booking.GuestSessionID != guestID {
		t.Fatalf("refused claim released the guest binding: %v", booking.GuestSessionID)
	}
	if booking.UserID != guestUserID {
		t.Fatalf("refused claim changed the owner: %s", booking.UserID)
	}
}

// TestGuestOrderClaimValidCookie: the happy path. Cookie-proven guest identity
// plus an authenticated account moves the order once, records the claimant, and
// hands access over from the guest path to the account path.
func TestGuestOrderClaimValidCookie(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	booking, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, guest.ID, "claim-key-0000000001", claimBookingReq(trip, 1))
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	account := seedClaimAccount(t, repo.DB, "owner-"+uuid.NewString()+"@example.com")

	result, err := svc.Guests.ClaimOrder(ctx, "guest-token", account.ID)
	if err != nil {
		t.Fatalf("claim should succeed: %v", err)
	}
	if !result.Transferred || result.BookingID != booking.ID {
		t.Fatalf("claim result wrong: %+v", result)
	}

	fresh := reloadBooking(t, repo.DB, booking.ID)
	if fresh.UserID != account.ID || fresh.GuestSessionID != nil {
		t.Fatalf("ownership not transferred: user=%s guest=%v", fresh.UserID, fresh.GuestSessionID)
	}
	claimed := reloadGuestSession(t, repo.DB, guest.ID)
	if claimed.ClaimedUserID == nil || *claimed.ClaimedUserID != account.ID || claimed.ClaimedAt == nil {
		t.Fatalf("claim marker not recorded: user=%v at=%v", claimed.ClaimedUserID, claimed.ClaimedAt)
	}
	if _, err := svc.Bookings.Find(ctx, booking.ID, account.ID, false); err != nil {
		t.Fatalf("account must access the claimed order: %v", err)
	}
	// The guest path closes in the same statement that transferred ownership.
	if _, err := svc.Bookings.FindGuest(ctx, booking.ID, guest.ID); err == nil {
		t.Fatal("guest cookie must no longer reach the claimed order")
	}
}

// TestGuestOrderClaimInvalidGuestIdentity: no cookie, an unknown cookie and an
// expired session are all "nothing to claim" — never a transfer, never a hard
// failure that would break the login the claim rides along with.
func TestGuestOrderClaimInvalidGuestIdentity(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	booking, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, guest.ID, "claim-key-0000000002", claimBookingReq(trip, 2))
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	account := seedClaimAccount(t, repo.DB, "invalid-"+uuid.NewString()+"@example.com")

	// Empty, unknown, and "the stored hash replayed as if it were the token".
	for _, token := range []string{"", "not-a-real-token", services.HashGuestToken("guest-token")} {
		result, err := svc.Guests.ClaimOrder(ctx, token, account.ID)
		if !errors.Is(err, services.ErrGuestOrderNothingToClaim) {
			t.Fatalf("token %q: expected nothing-to-claim, got %v", token, err)
		}
		if result.Transferred {
			t.Fatalf("token %q: reported a transfer", token)
		}
		assertStillGuestOwned(t, repo.DB, booking.ID, guest.ID, guestUser.ID)
	}

	// An expired guest session is not an identity any more, even though the raw
	// token still matches its hash.
	if err := repo.DB.Model(&models.GuestSession{}).Where("id = ?", guest.ID).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("expire guest session: %v", err)
	}
	if _, err := svc.Guests.ClaimOrder(ctx, "guest-token", account.ID); !errors.Is(err, services.ErrGuestOrderNothingToClaim) {
		t.Fatalf("expired session: expected nothing-to-claim, got %v", err)
	}
	assertStillGuestOwned(t, repo.DB, booking.ID, guest.ID, guestUser.ID)

	// No account at all is refused before any DB work: uuid.Nil must never end
	// up in bookings.user_id looking like a real owner.
	if _, err := svc.Guests.ClaimOrder(ctx, "guest-token", uuid.Nil); !errors.Is(err, services.ErrGuestOrderClaimUnauthenticated) {
		t.Fatalf("nil account: expected unauthenticated refusal, got %v", err)
	}
}

// TestGuestOrderClaimWrongGuest: guest B cannot reach guest A's order — not with
// its own cookie, and not even when B's session is doctored to point at A's
// booking (the "I know the order id" case). first_order_id is a pointer, not
// ownership: the booking must still reference the claiming session.
func TestGuestOrderClaimWrongGuest(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUserA, trip, guestA := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	bookingA, err := svc.Bookings.CreateGuest(ctx, guestUserA.ID, guestA.ID, "claim-key-0000000003", claimBookingReq(trip, 3))
	if err != nil {
		t.Fatalf("create guest A order: %v", err)
	}
	guestUserB, guestB := seedClaimGuestSession(t, repo.DB, "guest-token-b")
	accountB := seedClaimAccount(t, repo.DB, "b-"+uuid.NewString()+"@example.com")

	// B has no order of its own: nothing to claim, A untouched.
	if _, err := svc.Guests.ClaimOrder(ctx, "guest-token-b", accountB.ID); !errors.Is(err, services.ErrGuestOrderNothingToClaim) {
		t.Fatalf("expected nothing-to-claim for a guest without an order, got %v", err)
	}
	assertStillGuestOwned(t, repo.DB, bookingA.ID, guestA.ID, guestUserA.ID)

	// B points at A's booking (as an attacker who learned the order id would).
	if err := repo.DB.Model(&models.GuestSession{}).Where("id = ?", guestB.ID).
		Update("first_order_id", bookingA.ID).Error; err != nil {
		t.Fatalf("point guest B at booking A: %v", err)
	}
	if _, err := svc.Guests.ClaimOrder(ctx, "guest-token-b", accountB.ID); !errors.Is(err, services.ErrGuestOrderNothingToClaim) {
		t.Fatalf("cross-guest claim must be refused, got %v", err)
	}
	assertStillGuestOwned(t, repo.DB, bookingA.ID, guestA.ID, guestUserA.ID)
	if marker := reloadGuestSession(t, repo.DB, guestB.ID); marker.ClaimedUserID != nil {
		t.Fatalf("refused cross-guest claim recorded a claimant: %v", marker.ClaimedUserID)
	}

	// B's own order still claims normally: the refusal above was targeted, not
	// a blanket lockout.
	bookingB, err := svc.Bookings.CreateGuest(ctx, guestUserB.ID, guestB.ID, "claim-key-0000000004", claimBookingReq(trip, 4))
	if err != nil {
		t.Fatalf("create guest B order: %v", err)
	}
	if err := repo.DB.Model(&models.GuestSession{}).Where("id = ?", guestB.ID).
		Update("first_order_id", bookingB.ID).Error; err != nil {
		t.Fatalf("reset guest B pointer: %v", err)
	}
	result, err := svc.Guests.ClaimOrder(ctx, "guest-token-b", accountB.ID)
	if err != nil || !result.Transferred || result.BookingID != bookingB.ID {
		t.Fatalf("guest B should claim its own order: %+v err=%v", result, err)
	}
	assertStillGuestOwned(t, repo.DB, bookingA.ID, guestA.ID, guestUserA.ID)
}

// TestGuestOrderClaimWrongAuthenticatedUser: once an order belongs to account A,
// a second account logging in behind the same guest cookie is refused with an
// explicit conflict. Nothing moves, and the refusal is not silent.
func TestGuestOrderClaimWrongAuthenticatedUser(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	booking, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, guest.ID, "claim-key-0000000005", claimBookingReq(trip, 5))
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	accountA := seedClaimAccount(t, repo.DB, "first-"+uuid.NewString()+"@example.com")
	accountB := seedClaimAccount(t, repo.DB, "second-"+uuid.NewString()+"@example.com")

	if _, err := svc.Guests.ClaimOrder(ctx, "guest-token", accountA.ID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	result, err := svc.Guests.ClaimOrder(ctx, "guest-token", accountB.ID)
	if !errors.Is(err, services.ErrGuestOrderClaimConflict) {
		t.Fatalf("second account must be refused, got %v", err)
	}
	if result.Transferred {
		t.Fatal("refused claim reported a transfer")
	}

	fresh := reloadBooking(t, repo.DB, booking.ID)
	if fresh.UserID != accountA.ID || fresh.GuestSessionID != nil {
		t.Fatalf("order left its first owner: user=%s guest=%v", fresh.UserID, fresh.GuestSessionID)
	}
	if marker := reloadGuestSession(t, repo.DB, guest.ID); marker.ClaimedUserID == nil || *marker.ClaimedUserID != accountA.ID {
		t.Fatalf("claim marker overwritten: %v", marker.ClaimedUserID)
	}
	if _, err := svc.Bookings.Find(ctx, booking.ID, accountB.ID, false); err == nil {
		t.Fatal("refused account must not read the order")
	}
}

// TestGuestOrderClaimDuplicateIsIdempotent: replaying the claim with the same
// account succeeds and changes nothing — retries (double-submit, refreshed
// login, re-issued session) are safe without re-deciding ownership.
func TestGuestOrderClaimDuplicateIsIdempotent(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	booking, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, guest.ID, "claim-key-0000000006", claimBookingReq(trip, 6))
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	account := seedClaimAccount(t, repo.DB, "idem-"+uuid.NewString()+"@example.com")

	first, err := svc.Guests.ClaimOrder(ctx, "guest-token", account.ID)
	if err != nil || !first.Transferred {
		t.Fatalf("first claim: %+v err=%v", first, err)
	}
	marker := reloadGuestSession(t, repo.DB, guest.ID)

	for attempt := 0; attempt < 3; attempt++ {
		replay, err := svc.Guests.ClaimOrder(ctx, "guest-token", account.ID)
		if err != nil {
			t.Fatalf("replay %d must succeed: %v", attempt, err)
		}
		if replay.Transferred {
			t.Fatalf("replay %d transferred again", attempt)
		}
		if replay.BookingID != booking.ID {
			t.Fatalf("replay %d reported another order: %s", attempt, replay.BookingID)
		}
	}

	fresh := reloadBooking(t, repo.DB, booking.ID)
	if fresh.UserID != account.ID || fresh.GuestSessionID != nil {
		t.Fatalf("replays disturbed ownership: user=%s guest=%v", fresh.UserID, fresh.GuestSessionID)
	}
	after := reloadGuestSession(t, repo.DB, guest.ID)
	if after.ClaimedUserID == nil || *after.ClaimedUserID != account.ID {
		t.Fatalf("claim marker changed: %v", after.ClaimedUserID)
	}
	if marker.ClaimedAt == nil || after.ClaimedAt == nil || !after.ClaimedAt.Equal(*marker.ClaimedAt) {
		t.Fatalf("replay rewrote claimed_at: %v -> %v", marker.ClaimedAt, after.ClaimedAt)
	}
	var count int64
	repo.DB.Model(&models.Booking{}).Where("user_id = ?", account.ID).Count(&count)
	if count != 1 {
		t.Fatalf("replays produced %d owned bookings", count)
	}
}

// TestGuestOrderConcurrentClaimsTransferOnce: several accounts claiming the same
// guest order at once. Exactly one transfer happens; every other attempt either
// replays (same account) or is refused (other account), and the stored owner is
// the single winner.
func TestGuestOrderConcurrentClaimsTransferOnce(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	booking, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, guest.ID, "claim-key-0000000007", claimBookingReq(trip, 7))
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	accounts := make([]models.User, 4)
	for i := range accounts {
		accounts[i] = seedClaimAccount(t, repo.DB, fmt.Sprintf("race%d-%s@example.com", i, uuid.NewString()))
	}

	type outcome struct {
		userID      uuid.UUID
		transferred bool
		err         error
	}
	const attemptsPerAccount = 2
	var (
		mu       sync.Mutex
		outcomes []outcome
		wg       sync.WaitGroup
	)
	for _, account := range accounts {
		for attempt := 0; attempt < attemptsPerAccount; attempt++ {
			wg.Add(1)
			go func(userID uuid.UUID) {
				defer wg.Done()
				result, err := svc.Guests.ClaimOrder(ctx, "guest-token", userID)
				mu.Lock()
				outcomes = append(outcomes, outcome{userID: userID, transferred: result.Transferred, err: err})
				mu.Unlock()
			}(account.ID)
		}
	}
	wg.Wait()

	transfers := 0
	winner := uuid.Nil
	for _, o := range outcomes {
		switch {
		case o.err == nil && o.transferred:
			transfers++
			winner = o.userID
		case o.err == nil:
			// Replay by the winning account: allowed, changes nothing.
		case errors.Is(o.err, services.ErrGuestOrderClaimConflict):
			// Refused: another account already owns the order.
		default:
			t.Fatalf("unexpected claim error: %v", o.err)
		}
	}
	if transfers != 1 {
		t.Fatalf("expected exactly one ownership transfer, got %d", transfers)
	}
	for _, o := range outcomes {
		if o.err == nil && o.userID != winner {
			t.Fatalf("account %s got a success it did not win", o.userID)
		}
	}

	fresh := reloadBooking(t, repo.DB, booking.ID)
	if fresh.UserID != winner || fresh.GuestSessionID != nil {
		t.Fatalf("stored owner disagrees with the race winner: user=%s guest=%v winner=%s", fresh.UserID, fresh.GuestSessionID, winner)
	}
	marker := reloadGuestSession(t, repo.DB, guest.ID)
	if marker.ClaimedUserID == nil || *marker.ClaimedUserID != winner {
		t.Fatalf("claim marker disagrees with the race winner: %v", marker.ClaimedUserID)
	}
	var count int64
	repo.DB.Model(&models.Booking{}).Where("guest_session_id = ?", guest.ID).Count(&count)
	if count != 0 {
		t.Fatalf("%d bookings still guest-owned after the race", count)
	}
}

// TestGuestOrderClaimAlreadyClaimedWithoutMarker: an order claimed before the
// marker columns existed (or moved out-of-band) still cannot be re-claimed by a
// different account. The current owner is read from the booking and reported,
// never overwritten — the migration backfill is an optimization, not the
// security boundary.
func TestGuestOrderClaimAlreadyClaimedWithoutMarker(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	booking, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, guest.ID, "claim-key-0000000008", claimBookingReq(trip, 8))
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	owner := seedClaimAccount(t, repo.DB, "legacy-"+uuid.NewString()+"@example.com")
	attacker := seedClaimAccount(t, repo.DB, "legacy-attacker-"+uuid.NewString()+"@example.com")

	// Legacy state: booking already transferred, no claim marker recorded.
	if err := repo.DB.Model(&models.Booking{}).Where("id = ?", booking.ID).
		Updates(map[string]interface{}{"user_id": owner.ID, "guest_session_id": nil}).Error; err != nil {
		t.Fatalf("simulate pre-migration claim: %v", err)
	}

	result, err := svc.Guests.ClaimOrder(ctx, "guest-token", owner.ID)
	if err != nil {
		t.Fatalf("rightful owner must get an idempotent success: %v", err)
	}
	if result.Transferred || result.BookingID != booking.ID {
		t.Fatalf("legacy replay reported wrong outcome: %+v", result)
	}

	refused, err := svc.Guests.ClaimOrder(ctx, "guest-token", attacker.ID)
	if !errors.Is(err, services.ErrGuestOrderClaimConflict) {
		t.Fatalf("another account must be refused, got %v", err)
	}
	if refused.Transferred {
		t.Fatal("refused legacy claim reported a transfer")
	}
	if fresh := reloadBooking(t, repo.DB, booking.ID); fresh.UserID != owner.ID {
		t.Fatalf("legacy order changed hands: %s", fresh.UserID)
	}
}

// TestGuestOrderClaimIgnoresMatchingEmail: sharing the order's contact email
// grants nothing. The claim path never reads an email, so an account created
// with the victim's address gets neither the order nor read access to it — the
// cookie is the only proof, and the order id is not an input at all.
func TestGuestOrderClaimIgnoresMatchingEmail(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	const victimEmail = "victim@example.com"
	req := claimBookingReq(trip, 9)
	req.ContactEmail = victimEmail
	booking, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, guest.ID, "claim-key-0000000009", req)
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	// Same email on the account, no valid guest cookie in the attacker's browser.
	attacker := seedClaimAccount(t, repo.DB, victimEmail)

	for _, token := range []string{"", "guest-token-forged", uuid.NewString()} {
		if _, err := svc.Guests.ClaimOrder(ctx, token, attacker.ID); !errors.Is(err, services.ErrGuestOrderNothingToClaim) {
			t.Fatalf("email match must not enable a claim (token %q): %v", token, err)
		}
		assertStillGuestOwned(t, repo.DB, booking.ID, guest.ID, guestUser.ID)
	}
	// Knowing the order id gives no read access either.
	if _, err := svc.Bookings.Find(ctx, booking.ID, attacker.ID, false); err == nil {
		t.Fatal("email match must not grant order access")
	}

	// The rightful visitor (cookie holder) still claims it afterwards.
	legit := seedClaimAccount(t, repo.DB, "legit-"+uuid.NewString()+"@example.com")
	result, err := svc.Guests.ClaimOrder(ctx, "guest-token", legit.ID)
	if err != nil || !result.Transferred {
		t.Fatalf("cookie holder should claim the order: %+v err=%v", result, err)
	}
	if fresh := reloadBooking(t, repo.DB, booking.ID); fresh.UserID != legit.ID {
		t.Fatalf("order went to the wrong account: %s", fresh.UserID)
	}
}
