package services_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"gorm.io/gorm"
)

// TOCTOU / concurrency tests for the guest-order domain: order creation, order
// ownership, the claim, and guest identity resolution. Each case drives the real
// service stack against the shared SQLite harness.
//
// Harness limit (GO-P3-6): SQLite with MaxOpenConns(1) serializes transactions,
// so `SELECT ... FOR UPDATE` is not the thing being proven here. What IS proven
// is the logic that holds on Postgres regardless of who wins the lock: every
// decision is made by a single-winner conditional write (or by a unique index),
// and the loser re-reads the winner's committed state instead of overwriting it.

// seedChatSession creates an anonymous chat session — the row whose
// guest_session_id decides which guest identity owns orders created from a chat
// (MCP create_booking).
func seedChatSession(t *testing.T, db *gorm.DB) models.ChatSession {
	t.Helper()
	now := time.Now()
	expires := now.Add(7 * 24 * time.Hour)
	session := models.ChatSession{Title: "Guest chat", ExpiresAt: &expires, LastActivityAt: &now}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	return session
}

func reloadChatSession(t *testing.T, db *gorm.DB, id uuid.UUID) models.ChatSession {
	t.Helper()
	var session models.ChatSession
	if err := db.First(&session, "id = ?", id).Error; err != nil {
		t.Fatalf("reload chat session: %v", err)
	}
	return session
}

// TestConcurrentGuestOrderSameIdempotencyKeyCreatesOne: the same guest firing the
// same Idempotency-Key concurrently (double-submit, client retry storm) must end
// with exactly ONE order, and every caller must receive that same order — not a
// constraint error. The unique index on bookings.idempotency_key_hash decides;
// the loser re-reads the winner's row through the same owner-scoped lookup.
func TestConcurrentGuestOrderSameIdempotencyKeyCreatesOne(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestBookingService(repo)
	ctx := context.Background()

	const attempts = 6
	const key = "race-key-000000000001"
	var (
		mu       sync.Mutex
		ids      []uuid.UUID
		failures []error
		wg       sync.WaitGroup
	)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			booking, err := svc.CreateGuest(ctx, guestUser.ID, guest.ID, key,
				contactBookingReq(trip, "race@example.com", "081200099"))
			mu.Lock()
			if err != nil {
				failures = append(failures, err)
			} else {
				ids = append(ids, booking.ID)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("same-key concurrent creates must all replay, got %d failures: %v", len(failures), failures[0])
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("same key produced different bookings: %s vs %s", ids[0], id)
		}
	}
	if got := countRows(t, repo.DB, &models.Booking{}, ""); got != 1 {
		t.Fatalf("expected exactly one booking, got %d", got)
	}
	fresh := reloadGuestSession(t, repo.DB, guest.ID)
	if fresh.OrderCount != 1 || fresh.FirstOrderID == nil || *fresh.FirstOrderID != ids[0] {
		t.Fatalf("allowance consumed more than once: count=%d first=%v", fresh.OrderCount, fresh.FirstOrderID)
	}
	// One order ⇒ one anchor per supplied contact channel, never duplicated.
	if got := countRows(t, repo.DB, &models.GuestOrderEntitlement{}, ""); got != 2 {
		t.Fatalf("expected email + phone anchors exactly once, got %d", got)
	}
}

// TestConcurrentDuplicateClaimsBySameAccount: the SAME account claiming the same
// guest order many times at once (retry loop, two tabs finishing login
// together). Exactly one call may transfer ownership; the rest are idempotent
// replays. No call may fail, and the recorded claimant must not be rewritten.
func TestConcurrentDuplicateClaimsBySameAccount(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	booking, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, guest.ID, "race-claim-0000000001", claimBookingReq(trip, 21))
	if err != nil {
		t.Fatalf("create guest order: %v", err)
	}
	account := seedClaimAccount(t, repo.DB, "dup-"+uuid.NewString()+"@example.com")

	const attempts = 8
	var (
		mu        sync.Mutex
		transfers int
		replays   int
		claimErrs []error
		wg        sync.WaitGroup
	)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := svc.Guests.ClaimOrder(ctx, "guest-token", account.ID)
			mu.Lock()
			switch {
			case err != nil:
				claimErrs = append(claimErrs, err)
			case result.Transferred:
				transfers++
			default:
				replays++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(claimErrs) > 0 {
		t.Fatalf("duplicate claims by the owner must not fail: %v", claimErrs[0])
	}
	if transfers != 1 {
		t.Fatalf("expected exactly one ownership transfer, got %d (replays=%d)", transfers, replays)
	}
	fresh := reloadBooking(t, repo.DB, booking.ID)
	if fresh.UserID != account.ID || fresh.GuestSessionID != nil {
		t.Fatalf("ownership inconsistent after concurrent duplicates: user=%s guest=%v", fresh.UserID, fresh.GuestSessionID)
	}
	marker := reloadGuestSession(t, repo.DB, guest.ID)
	if marker.ClaimedUserID == nil || *marker.ClaimedUserID != account.ID {
		t.Fatalf("claim marker wrong: %v", marker.ClaimedUserID)
	}
}

// TestConcurrentGuestIdentityResolutionIsStable: concurrent requests carrying the
// SAME live guest cookie must all resolve to the SAME guest session. Minting a
// second identity here would hand out a second one-order allowance and split the
// visitor's order ownership across two identities.
func TestConcurrentGuestIdentityResolutionIsStable(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	_, _, guest := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	const attempts = 8
	var (
		mu       sync.Mutex
		resolved []services.GuestIdentity
		errs     []error
		wg       sync.WaitGroup
	)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identity, err := svc.Guests.Resolve(ctx, "guest-token")
			mu.Lock()
			if err != nil {
				errs = append(errs, err)
			} else {
				resolved = append(resolved, identity)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("resolve must not fail: %v", errs[0])
	}
	for _, identity := range resolved {
		if identity.Session.ID != guest.ID {
			t.Fatalf("resolved a different identity: %s want %s", identity.Session.ID, guest.ID)
		}
		if identity.IsNew {
			t.Fatal("a live cookie must never mint a new identity")
		}
	}
	if got := countRows(t, repo.DB, &models.GuestSession{}, ""); got != 1 {
		t.Fatalf("expected one guest session, got %d", got)
	}
}

// TestChatGuestBindingIsNotStolenByAnotherIdentity: chat_sessions.guest_session_id
// is an authorization input — the MCP guest branch derives the OWNER of a new
// order from it — so a second identity may not re-point an already bound chat.
// The refusal is explicit, the column keeps the first binding, and an order
// created from that chat still belongs to the first identity.
func TestChatGuestBindingIsNotStolenByAnotherIdentity(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	_, trip, guestA := seedGuestFixture(t, repo.DB)
	_, guestB := newGuestIdentity(t, repo.DB, "thief")
	svc := newGuestClaimServices(repo)
	chat := seedChatSession(t, repo.DB)
	ctx := context.Background()

	if err := svc.Guests.AttachChat(ctx, chat.ID, guestA.ID); err != nil {
		t.Fatalf("first bind must win: %v", err)
	}
	// Idempotent re-bind by the same identity (every chat request does this).
	if err := svc.Guests.AttachChat(ctx, chat.ID, guestA.ID); err != nil {
		t.Fatalf("re-bind by the owner must stay a no-op success: %v", err)
	}
	if err := svc.Guests.AttachChat(ctx, chat.ID, guestB.ID); !errors.Is(err, services.ErrChatSessionGuestMismatch) {
		t.Fatalf("foreign identity must be refused, got %v", err)
	}
	bound := reloadChatSession(t, repo.DB, chat.ID)
	if bound.GuestSessionID == nil || *bound.GuestSessionID != guestA.ID {
		t.Fatalf("binding was stolen: %v", bound.GuestSessionID)
	}

	// Derive the order owner exactly like the MCP guest branch does
	// (chat_sessions.guest_session_id → guest_sessions → CreateGuest).
	owner, err := repo.FindGuestSession(ctx, *bound.GuestSessionID)
	if err != nil {
		t.Fatalf("resolve bound guest: %v", err)
	}
	booking, err := svc.Bookings.CreateGuest(ctx, owner.UserID, owner.ID, "bind-key-00000000001",
		contactBookingReq(trip, "bound@example.com", "081200077"))
	if err != nil {
		t.Fatalf("create order for the bound identity: %v", err)
	}
	if booking.GuestSessionID == nil || *booking.GuestSessionID != guestA.ID {
		t.Fatalf("order attributed to the wrong identity: %v", booking.GuestSessionID)
	}
	if spent := reloadGuestSession(t, repo.DB, guestB.ID); spent.OrderCount != 0 {
		t.Fatalf("refused identity had its allowance spent: %d", spent.OrderCount)
	}
}

// TestConcurrentChatGuestBindHasSingleWinner: several identities binding the same
// chat session at once. Exactly one wins; the stored binding agrees with it.
func TestConcurrentChatGuestBindHasSingleWinner(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	svc := newGuestClaimServices(repo)
	chat := seedChatSession(t, repo.DB)
	ctx := context.Background()

	identities := make([]models.GuestSession, 5)
	for i := range identities {
		_, identities[i] = newGuestIdentity(t, repo.DB, "race")
	}

	var (
		mu      sync.Mutex
		winners []uuid.UUID
		other   []error
		wg      sync.WaitGroup
	)
	for _, identity := range identities {
		wg.Add(1)
		go func(guestID uuid.UUID) {
			defer wg.Done()
			err := svc.Guests.AttachChat(ctx, chat.ID, guestID)
			mu.Lock()
			switch {
			case err == nil:
				winners = append(winners, guestID)
			case errors.Is(err, services.ErrChatSessionGuestMismatch):
				// Lost the bind: correct, the chat already has an owner.
			default:
				other = append(other, err)
			}
			mu.Unlock()
		}(identity.ID)
	}
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected bind error: %v", other[0])
	}
	if len(winners) != 1 {
		t.Fatalf("expected exactly one winning bind, got %d", len(winners))
	}
	bound := reloadChatSession(t, repo.DB, chat.ID)
	if bound.GuestSessionID == nil || *bound.GuestSessionID != winners[0] {
		t.Fatalf("stored binding disagrees with the winner: %v want %s", bound.GuestSessionID, winners[0])
	}
}

// TestChatGuestBindingTakenOverWhenOwnerExpired: a chat session bound to an
// EXPIRED guest identity may be re-bound. A dead identity can no longer create,
// read, or claim orders (every path resolves the cookie hash against live
// sessions only), so the takeover grants nothing — it only keeps a long-lived
// chat usable after the 30-day identity TTL.
func TestChatGuestBindingTakenOverWhenOwnerExpired(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	_, _, guestA := seedGuestFixture(t, repo.DB)
	_, guestB := newGuestIdentity(t, repo.DB, "successor")
	svc := newGuestClaimServices(repo)
	chat := seedChatSession(t, repo.DB)
	ctx := context.Background()

	if err := svc.Guests.AttachChat(ctx, chat.ID, guestA.ID); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if err := repo.DB.Model(&models.GuestSession{}).Where("id = ?", guestA.ID).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("expire guest A: %v", err)
	}
	if err := svc.Guests.AttachChat(ctx, chat.ID, guestB.ID); err != nil {
		t.Fatalf("expired owner must not block a new binding: %v", err)
	}
	bound := reloadChatSession(t, repo.DB, chat.ID)
	if bound.GuestSessionID == nil || *bound.GuestSessionID != guestB.ID {
		t.Fatalf("binding not handed over: %v", bound.GuestSessionID)
	}
}
