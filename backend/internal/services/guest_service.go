package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"gorm.io/gorm"
)

var ErrGuestSessionInvalid = errors.New("guest session invalid or expired")

// ErrChatSessionGuestMismatch: the chat session presented by the caller is
// already bound to a DIFFERENT, still-live guest identity. The binding decides
// who owns orders created from that chat (MCP `create_booking` reads
// chat_sessions.guest_session_id), so it is never re-pointed; the caller gets a
// fresh session instead.
var ErrChatSessionGuestMismatch = errors.New("chat session belongs to another guest identity")

// Guest-order claim outcomes (GO-P1-3 / GO-P3-3). ClaimOrder used to return a
// bare nil for "nothing to claim" and a raw gorm error for everything else, so
// its three call sites (Register, Login, Google callback) could not tell a
// normal no-op apart from a real failure or from a refusal. These sentinels make
// each outcome explicit and auditable.
var (
	// ErrGuestOrderNothingToClaim: no guest cookie, an unknown/expired guest
	// session, or a session that never placed an order. Expected on most
	// logins; never a failure.
	ErrGuestOrderNothingToClaim = errors.New("no guest order to claim")
	// ErrGuestOrderClaimConflict: the guest order is already owned by a
	// DIFFERENT account. Refused — a claim links an unclaimed order to its
	// first claimant, it is not a transfer instrument between accounts.
	ErrGuestOrderClaimConflict = errors.New("guest order already claimed by another account")
	// ErrGuestOrderClaimUnauthenticated: no account to claim to (uuid.Nil).
	// Refused before touching the database, otherwise the nil UUID would be
	// written into bookings.user_id and look like a real owner.
	ErrGuestOrderClaimUnauthenticated = errors.New("guest order claim requires an authenticated account")
)

// Security audit events for the claim transition. Payloads carry only
// non-secret identifiers plus a category reason (coding-rules §1.6): never the
// guest token, its hash, or contact PII.
const (
	eventGuestOrderLinked        = "guest_order_linked"
	eventGuestOrderClaimReplayed = "guest_order_claim_replayed"
	eventGuestOrderClaimConflict = "guest_order_claim_conflict"
	eventGuestOrderClaimFailed   = "guest_order_claim_failed"
	// eventGuestChatBindRefused fires when a chat session could not be bound
	// because it already belongs to another live guest identity (GO-P2-7) —
	// the shape of a copied chat cookie or two identities racing in one browser.
	eventGuestChatBindRefused = "guest_chat_bind_refused"
)

// Reason categories attached to eventGuestOrderClaimFailed.
const (
	guestClaimFailReasonNoAccount   = "no_authenticated_account"
	guestClaimFailReasonBadIdentity = "guest_identity_invalid"
	guestClaimFailReasonRepository  = "repository_error"
)

type GuestService struct {
	repo  repositories.GuestRepository
	cfg   config.Config
	users GuestUserProvider
}

type GuestIdentity struct {
	Session models.GuestSession
	Token   string
	IsNew   bool
}

// GuestOrderClaimResult tells the caller what happened to the guest order.
// Transferred is false for an idempotent replay: this account already owned the
// order, so the call changed nothing and is still a success.
type GuestOrderClaimResult struct {
	BookingID   uuid.UUID
	Transferred bool
}

func HashGuestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *GuestService) Resolve(ctx context.Context, token string) (GuestIdentity, error) {
	if token != "" {
		session, err := s.repo.FindGuestSessionByTokenHash(ctx, HashGuestToken(token))
		if err == nil {
			return GuestIdentity{Session: session, Token: token}, nil
		}
	}

	ttl := s.cfg.GuestIdentityTTL
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	// Retry on the (astronomically unlikely) token-hash collision or a parallel
	// request that just created the same hash: the second Create hits the
	// unique constraint, then the read-after-collision resolves the winner's row.
	for attempt := 0; attempt < 3; attempt++ {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return GuestIdentity{}, err
		}
		token = hex.EncodeToString(raw)
		user, err := s.users.GuestUser(ctx)
		if err != nil {
			return GuestIdentity{}, err
		}
		session := models.GuestSession{TokenHash: HashGuestToken(token), UserID: user.ID, ExpiresAt: time.Now().Add(ttl)}
		if err := s.repo.CreateGuestSession(ctx, &session); err != nil {
			// Unique-constraint collision (parallel create won the race): resolve
			// the existing session for the same token instead of failing. This
			// also covers a cookie that was cleared between Resolve calls but a
			// session was created concurrently.
			if existing, ferr := s.repo.FindGuestSessionByTokenHash(ctx, HashGuestToken(token)); ferr == nil {
				return GuestIdentity{Session: existing, Token: token}, nil
			}
			continue
		}
		return GuestIdentity{Session: session, Token: token, IsNew: true}, nil
	}
	return GuestIdentity{}, errors.New("failed to create guest session")
}

func (s *GuestService) Authenticate(ctx context.Context, token string) (models.GuestSession, error) {
	if token == "" {
		return models.GuestSession{}, ErrGuestSessionInvalid
	}
	session, err := s.repo.FindGuestSessionByTokenHash(ctx, HashGuestToken(token))
	if err != nil {
		return models.GuestSession{}, ErrGuestSessionInvalid
	}
	return session, nil
}

// AttachChat binds the anonymous chat session to the guest identity that proved
// ownership on THIS request (the HttpOnly cookie). The binding is not cosmetic:
// MCP `create_booking` reads chat_sessions.guest_session_id to decide which
// guest identity owns the order it creates, so a blind overwrite let any later
// request re-point an existing chat at a different identity — spending that
// identity's one-order allowance and attributing the order to it (GO-P2-7).
//
// The repository performs a single-winner conditional UPDATE. A refusal means
// the chat session is owned by another LIVE guest identity; the caller must
// start a fresh chat session instead of reusing it.
func (s *GuestService) AttachChat(ctx context.Context, chatID, guestID uuid.UUID) error {
	bound, err := s.repo.BindChatSessionGuest(ctx, chatID, guestID)
	if err != nil {
		return err
	}
	if !bound {
		auth.LogSecurity(eventGuestChatBindRefused, map[string]any{
			"chat_session_id":  chatID.String(),
			"guest_session_id": guestID.String(),
		})
		return ErrChatSessionGuestMismatch
	}
	return nil
}

// ClaimOrder moves the single order a guest session placed to the account that
// just authenticated (password login/register or Google OAuth callback).
//
// What proves the claim — and what deliberately does NOT:
//   - PROOF: possession of the HttpOnly guest cookie, whose SHA-256 digest
//     resolves the guest session row that the order is anchored to. That row is
//     the only thing consulted to pick the booking.
//   - NOT proof: a booking id (the caller cannot pass one), a matching contact
//     email (never read here — no auto-claim by email exists), or being any
//     authenticated user (a user without the cookie claims nothing).
//
// Outcomes, all distinguishable by the caller:
//   - (result, nil) with Transferred=true  → ownership moved in this call.
//   - (result, nil) with Transferred=false → idempotent replay; this account
//     already owned the order, nothing was written.
//   - ErrGuestOrderNothingToClaim          → normal no-op (no cookie, invalid
//     session, or the session never ordered).
//   - ErrGuestOrderClaimConflict           → order belongs to ANOTHER account;
//     refused and audited, never silently transferred.
//   - ErrGuestOrderClaimUnauthenticated    → no account to claim to.
//   - anything else                        → repository failure, audited.
func (s *GuestService) ClaimOrder(ctx context.Context, token string, userID uuid.UUID) (GuestOrderClaimResult, error) {
	if token == "" {
		return GuestOrderClaimResult{}, ErrGuestOrderNothingToClaim
	}
	if userID == uuid.Nil {
		auth.LogSecurity(eventGuestOrderClaimFailed, map[string]any{"reason": guestClaimFailReasonNoAccount})
		return GuestOrderClaimResult{}, ErrGuestOrderClaimUnauthenticated
	}
	guest, err := s.Authenticate(ctx, token)
	if err != nil {
		// A cookie was presented but does not resolve to a live guest session:
		// stale, already expired, or forged. Not fatal to the login, but worth
		// a trail — this is the shape a guessing attempt would take.
		auth.LogSecurity(eventGuestOrderClaimFailed, map[string]any{
			"reason":  guestClaimFailReasonBadIdentity,
			"user_id": userID.String(),
		})
		return GuestOrderClaimResult{}, ErrGuestOrderNothingToClaim
	}
	if guest.FirstOrderID == nil {
		return GuestOrderClaimResult{}, ErrGuestOrderNothingToClaim
	}
	claim, err := s.repo.ClaimGuestOrder(ctx, guest.ID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// The guest row/booking stopped qualifying between Authenticate and
			// the locked read (expiry, or first_order_id pointing at another
			// guest's booking). Nothing was moved.
			return GuestOrderClaimResult{}, ErrGuestOrderNothingToClaim
		}
		auth.LogSecurity(eventGuestOrderClaimFailed, map[string]any{
			"reason":           guestClaimFailReasonRepository,
			"guest_session_id": guest.ID.String(),
			"user_id":          userID.String(),
		})
		return GuestOrderClaimResult{}, err
	}
	result := GuestOrderClaimResult{BookingID: claim.BookingID, Transferred: claim.Transferred}
	if claim.OwnerID != userID {
		// Ownership was decided by an earlier claim and is not revisited. This
		// is the wrong-account case: another visitor's browser, a second
		// account logging in behind the same guest cookie, or a stolen cookie.
		auth.LogSecurity(eventGuestOrderClaimConflict, map[string]any{
			"guest_session_id": guest.ID.String(),
			"booking_id":       claim.BookingID.String(),
			"user_id":          userID.String(),
			"owner_user_id":    claim.OwnerID.String(),
		})
		return GuestOrderClaimResult{BookingID: claim.BookingID}, ErrGuestOrderClaimConflict
	}
	if !claim.Transferred {
		// Same account, already the owner: idempotent success. Logged as a
		// replay so a retry loop is visible without looking like a new link.
		auth.LogSecurity(eventGuestOrderClaimReplayed, map[string]any{
			"guest_session_id": guest.ID.String(),
			"booking_id":       claim.BookingID.String(),
			"user_id":          userID.String(),
		})
		return result, nil
	}
	auth.LogSecurity(eventGuestOrderLinked, map[string]any{
		"guest_session_id": guest.ID.String(),
		"booking_id":       claim.BookingID.String(),
		"user_id":          userID.String(),
	})
	return result, nil
}
