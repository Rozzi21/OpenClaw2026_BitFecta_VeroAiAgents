package services_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// Idempotency across the guest → account boundary (GO-P2-4).
//
// bookings.idempotency_key_hash binds an Idempotency-Key to its OWNER:
// sha256("guest:"+guestSessionID+":"+key) while ordering as a guest,
// sha256("user:"+userID+":"+key) once signed in. That scoping is what stops two
// unrelated callers who happen to pick the same key from replaying each other's
// order — but the claim moves the booking to an account WITHOUT rehashing it (the
// raw key is never stored, so it cannot be rehashed).
//
// The consequence the audit flagged: the account replaying the very request it
// had just made as a guest looked like a brand new logical request and created a
// SECOND order. That replay is not hypothetical — it is exactly what a client
// does after the first attempt came back 403 GUEST_ORDER_LIMIT_REACHED and the
// user signed in, and the authenticated path has no one-order rule to stop it.
//
// BookingService.create therefore also looks the key up under the guest scopes
// this account has already claimed (guest_sessions.claimed_user_id), while
// keeping the owner filter on the caller. These tests pin both halves: the
// duplicate is gone, and the extra lookup grants nothing new.

func TestClaimedGuestIdempotencyKeyIsNotReplayableAsSecondOrder(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	const key = "claim-idem-000000000001"
	req := claimBookingReq(trip, 901)
	guestOrder, err := svc.CreateGuest(ctx, guestUser.ID, guest.ID, key, req)
	if err != nil {
		t.Fatalf("guest order: %v", err)
	}

	account := seedClaimAccount(t, repo.DB, "claimant-"+uuid.NewString()+"@example.com")
	claim, err := repo.ClaimGuestOrder(ctx, guest.ID, account.ID)
	if err != nil {
		t.Fatalf("claim guest order: %v", err)
	}
	if !claim.Transferred || claim.BookingID != guestOrder.ID {
		t.Fatalf("claim did not transfer the guest order: %+v", claim)
	}

	// The same logical request, replayed by the same human, now authenticated.
	replay, err := svc.Create(ctx, account.ID, key, req)
	if err != nil {
		t.Fatalf("authenticated replay of the claimed key: %v", err)
	}
	if replay.ID != guestOrder.ID {
		t.Fatalf("replay created a duplicate order: %s (claimed order %s)", replay.ID, guestOrder.ID)
	}
	if count := countRows(t, repo.DB, &models.Booking{}, "user_id = ?", account.ID); count != 1 {
		t.Fatalf("account ended up with %d orders after replaying one key", count)
	}
	if count := countRows(t, repo.DB, &models.Booking{}, "trip_id = ?", trip.ID); count != 1 {
		t.Fatalf("%d bookings exist for one logical request", count)
	}
}

// TestClaimedGuestIdempotencyReplayStaysScopedToTheClaimant: the extra lookup is
// keyed by the claims made BY the caller, so a different account presenting the
// same key gets its own new order and never reads the claimed one.
func TestClaimedGuestIdempotencyReplayStaysScopedToTheClaimant(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	const key = "claim-idem-000000000002"
	guestOrder, err := svc.CreateGuest(ctx, guestUser.ID, guest.ID, key, claimBookingReq(trip, 902))
	if err != nil {
		t.Fatalf("guest order: %v", err)
	}
	claimant := seedClaimAccount(t, repo.DB, "claimant-"+uuid.NewString()+"@example.com")
	if _, err := repo.ClaimGuestOrder(ctx, guest.ID, claimant.ID); err != nil {
		t.Fatalf("claim guest order: %v", err)
	}

	stranger := seedClaimAccount(t, repo.DB, "stranger-"+uuid.NewString()+"@example.com")
	other, err := svc.Create(ctx, stranger.ID, key, claimBookingReq(trip, 903))
	if err != nil {
		t.Fatalf("unrelated account with the same key: %v", err)
	}
	if other.ID == guestOrder.ID {
		t.Fatal("another account replayed the claimed order")
	}
	if other.UserID != stranger.ID {
		t.Fatalf("new order attributed to %s, want %s", other.UserID, stranger.ID)
	}
	if owner := reloadBooking(t, repo.DB, guestOrder.ID); owner.UserID != claimant.ID {
		t.Fatalf("claimed order changed owner to %s", owner.UserID)
	}
}

// TestClaimedGuestIdempotencyGuardOnlyMatchesTheSameKey: the guard must not turn
// into a per-account order lock. A different key from the same account is a
// different logical request and still creates a new order.
func TestClaimedGuestIdempotencyGuardOnlyMatchesTheSameKey(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	guestOrder, err := svc.CreateGuest(ctx, guestUser.ID, guest.ID, "claim-idem-000000000003", claimBookingReq(trip, 904))
	if err != nil {
		t.Fatalf("guest order: %v", err)
	}
	account := seedClaimAccount(t, repo.DB, "claimant-"+uuid.NewString()+"@example.com")
	if _, err := repo.ClaimGuestOrder(ctx, guest.ID, account.ID); err != nil {
		t.Fatalf("claim guest order: %v", err)
	}

	fresh, err := svc.Create(ctx, account.ID, "claim-idem-000000000004", claimBookingReq(trip, 905))
	if err != nil {
		t.Fatalf("new key must create a new order: %v", err)
	}
	if fresh.ID == guestOrder.ID {
		t.Fatal("a different key replayed the claimed order")
	}
	if count := countRows(t, repo.DB, &models.Booking{}, "user_id = ?", account.ID); count != 2 {
		t.Fatalf("expected two orders for the account, got %d", count)
	}
}

// TestUnclaimedGuestKeyIsNotVisibleToAnAccount: without a claim marker there is
// no link between the guest scope and the account, and none is invented — the
// account's order is its own and the guest order stays where it was.
func TestUnclaimedGuestKeyIsNotVisibleToAnAccount(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	const key = "claim-idem-000000000005"
	guestOrder, err := svc.CreateGuest(ctx, guestUser.ID, guest.ID, key, claimBookingReq(trip, 906))
	if err != nil {
		t.Fatalf("guest order: %v", err)
	}
	account := seedClaimAccount(t, repo.DB, "never-claimed-"+uuid.NewString()+"@example.com")
	own, err := svc.Create(ctx, account.ID, key, claimBookingReq(trip, 907))
	if err != nil {
		t.Fatalf("account order: %v", err)
	}
	if own.ID == guestOrder.ID {
		t.Fatal("an unclaimed guest order was served to an account")
	}
	booking := reloadBooking(t, repo.DB, guestOrder.ID)
	if booking.GuestSessionID == nil || *booking.GuestSessionID != guest.ID {
		t.Fatalf("guest order lost its guest binding: %v", booking.GuestSessionID)
	}
}
