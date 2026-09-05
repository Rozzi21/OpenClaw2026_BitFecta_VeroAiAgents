package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

// Expired guest identity — deliberate policy, verified boundaries.
//
// POLICY: a chat session whose guest identity has EXPIRED may be re-bound to a
// new guest identity (repositories.BindChatSessionGuest, GO-P2-7). Without it a
// long-lived chat would be permanently unusable after the 30-day
// GUEST_IDENTITY_TTL_HOURS: every later request would be refused with
// ErrChatSessionGuestMismatch and the visitor would be pushed into a brand new
// chat with no history.
//
// The policy is only defensible if the takeover grants NOTHING. This test pins
// the three properties that make that true, so a future change to the binding
// rule cannot quietly turn it into an access path:
//
//  1. no read access to the expired identity's order;
//  2. no bypass of the one-order rule (the contact anchor is still spent);
//  3. no claim of the expired identity's order — neither by the successor
//     identity nor by the expired token itself.
//
// See docs/GUEST_ORDER_LIMIT.md §"Kebijakan identitas guest kedaluwarsa".
func TestExpiredGuestIdentityTakeoverGrantsNothing(t *testing.T) {
	_, repo := setupGuestTestDB(t)
	guestUser, trip, expiring := seedGuestFixture(t, repo.DB)
	svc := newGuestClaimServices(repo)
	ctx := context.Background()

	const (
		expiredToken   = "guest-token" // the token seedGuestFixture hashes
		successorToken = "successor-token"
		orderEmail     = "expired-owner@example.com"
		orderPhone     = "081200099001"
	)

	order, err := svc.Bookings.CreateGuest(ctx, guestUser.ID, expiring.ID, "expired-idem-00000001",
		contactBookingReq(trip, orderEmail, orderPhone))
	if err != nil {
		t.Fatalf("guest order: %v", err)
	}

	chat := seedChatSession(t, repo.DB)
	if err := svc.Guests.AttachChat(ctx, chat.ID, expiring.ID); err != nil {
		t.Fatalf("bind chat to the ordering identity: %v", err)
	}

	// The identity (and its cookie) reach the end of their TTL.
	if err := repo.DB.Model(&models.GuestSession{}).Where("id = ?", expiring.ID).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("expire guest identity: %v", err)
	}

	_, successor := seedClaimGuestSession(t, repo.DB, successorToken)
	if err := svc.Guests.AttachChat(ctx, chat.ID, successor.ID); err != nil {
		t.Fatalf("expired owner must not block a new binding: %v", err)
	}
	if bound := reloadChatSession(t, repo.DB, chat.ID); bound.GuestSessionID == nil || *bound.GuestSessionID != successor.ID {
		t.Fatalf("binding not handed over: %v", bound.GuestSessionID)
	}

	// (1) The chat came with history, not with the order.
	if _, err := svc.Bookings.FindGuest(ctx, order.ID, successor.ID); !errors.Is(err, services.ErrBookingNotFound) {
		t.Fatalf("successor identity read the expired identity's order: %v", err)
	}

	// (2) The one-order rule is anchored on the contact, not on the identity:
	// the successor cannot re-order with the same contact.
	if _, err := svc.Bookings.CreateGuest(ctx, successor.UserID, successor.ID, "expired-idem-00000002",
		contactBookingReq(trip, orderEmail, orderPhone)); !errors.Is(err, services.ErrGuestOrderLimitReached) {
		t.Fatalf("takeover reset the one-order rule: %v", err)
	}
	if spent := reloadGuestSession(t, repo.DB, successor.ID); spent.OrderCount != 0 {
		t.Fatalf("refused order consumed the successor's allowance: %d", spent.OrderCount)
	}

	// (3) Neither the successor nor the expired token can claim the order.
	account := seedClaimAccount(t, repo.DB, "takeover-"+uuid.NewString()+"@example.com")
	if _, err := svc.Guests.ClaimOrder(ctx, successorToken, account.ID); !errors.Is(err, services.ErrGuestOrderNothingToClaim) {
		t.Fatalf("successor identity claimed the expired identity's order: %v", err)
	}
	if _, err := svc.Guests.ClaimOrder(ctx, expiredToken, account.ID); !errors.Is(err, services.ErrGuestOrderNothingToClaim) {
		t.Fatalf("expired token still claims: %v", err)
	}
	assertStillGuestOwned(t, repo.DB, order.ID, expiring.ID, guestUser.ID)
	if marker := reloadGuestSession(t, repo.DB, expiring.ID); marker.ClaimedUserID != nil {
		t.Fatalf("refused claim wrote a claim marker: %v", marker.ClaimedUserID)
	}
}
