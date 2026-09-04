package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) CreateGuestSession(ctx context.Context, session *models.GuestSession) error {
	return r.DB.WithContext(ctx).Create(session).Error
}

func (r *Repository) FindGuestSessionByTokenHash(ctx context.Context, hash string) (models.GuestSession, error) {
	var session models.GuestSession
	err := r.DB.WithContext(ctx).First(&session, "token_hash = ? AND expires_at > ?", hash, time.Now()).Error
	return session, err
}

func (r *Repository) FindGuestSession(ctx context.Context, id uuid.UUID) (models.GuestSession, error) {
	var session models.GuestSession
	err := r.DB.WithContext(ctx).First(&session, "id = ? AND expires_at > ?", id, time.Now()).Error
	return session, err
}

// BindChatSessionGuest binds an anonymous chat session to a guest identity with
// a single conditional UPDATE (GO-P2-7). It replaced a blind
// `UPDATE chat_sessions SET guest_session_id = ?` because that write is an
// authorization input, not a hint: MCP `create_booking` derives the OWNER of a
// guest order from chat_sessions.guest_session_id
// (`mcp_service.go` guest branch), so whoever last wrote this column decided
// whose entitlement was spent and whose order it became.
//
// The row is (re)bound only when it is not already owned by a different LIVE
// guest identity:
//   - guest_session_id IS NULL — first bind;
//   - guest_session_id = the same guest — idempotent re-bind on every request;
//   - the bound guest session no longer exists or has expired — a dead identity
//     cannot own anything, and taking the row over grants no access to its old
//     order (order reads/claims resolve the cookie hash against LIVE sessions
//     only).
//
// Predicate and write are one statement, so two concurrent binds cannot both
// win: Postgres re-evaluates the predicate on the updated row version after the
// row lock is released, and the loser reports rowsAffected == 0. Same
// single-winner shape as ConsumeGuestOrder / ConsumeOAuthState.
func (r *Repository) BindChatSessionGuest(ctx context.Context, chatID, guestID uuid.UUID) (bool, error) {
	result := r.DB.WithContext(ctx).Model(&models.ChatSession{}).
		Where(`id = ? AND (
			guest_session_id IS NULL
			OR guest_session_id = ?
			OR NOT EXISTS (
				SELECT 1 FROM guest_sessions gs
				WHERE gs.id = chat_sessions.guest_session_id AND gs.expires_at > ?
			)
		)`, chatID, guestID, time.Now()).
		Update("guest_session_id", guestID)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// WithBookingTransaction exposes a repository-only transaction boundary while
// keeping GORM out of the service layer. The callback receives the same narrow
// contract backed by the transaction handle.
func (r *Repository) WithBookingTransaction(ctx context.Context, fn func(BookingTransactionRepository) error) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{DB: tx})
	})
}

func (r *Repository) LockGuestSession(ctx context.Context, id uuid.UUID) (models.GuestSession, error) {
	var session models.GuestSession
	err := r.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "id = ? AND expires_at > ?", id, time.Now()).Error
	return session, err
}

func (r *Repository) ConsumeGuestOrder(ctx context.Context, guestID, bookingID uuid.UUID) error {
	result := r.DB.WithContext(ctx).Model(&models.GuestSession{}).
		Where("id = ? AND order_count = 0", guestID).
		Updates(map[string]interface{}{"order_count": 1, "first_order_id": bookingID})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// FindGuestOrderEntitlement resolves the first guest order already anchored to
// any of the given contact keys (GO-P0-1). A hit means some visitor — possibly
// the same person behind a freshly minted guest identity — already spent the
// single guest order, so the caller must refuse instead of handing out another.
// A miss is reported as gorm.ErrRecordNotFound, matching the repository's
// existing lookup convention.
func (r *Repository) FindGuestOrderEntitlement(ctx context.Context, contactKeys []string) (models.GuestOrderEntitlement, error) {
	var entitlement models.GuestOrderEntitlement
	if len(contactKeys) == 0 {
		return entitlement, gorm.ErrRecordNotFound
	}
	err := r.DB.WithContext(ctx).First(&entitlement, "contact_key IN ?", contactKeys).Error
	return entitlement, err
}

// ConsumeGuestOrderEntitlements records the contact anchors of a successful
// guest order (GO-P0-1). The unique index on contact_key — not this Go code —
// is the authoritative gate: the INSERT is emitted with ON CONFLICT DO NOTHING,
// so a key that is already taken affects zero rows and surfaces as
// gorm.ErrDuplicatedKey instead of aborting the surrounding transaction. The
// caller maps that to the guest-order-limit error, which rolls the booking
// INSERT back as well, so a rejected attempt never leaves a half-consumed
// entitlement behind.
func (r *Repository) ConsumeGuestOrderEntitlements(ctx context.Context, entitlements []models.GuestOrderEntitlement) error {
	for i := range entitlements {
		result := r.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&entitlements[i])
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrDuplicatedKey
		}
	}
	return nil
}

func (r *Repository) FindBookingByIdempotency(ctx context.Context, ownerID uuid.UUID, guest bool, hash string) (models.Booking, error) {
	var booking models.Booking
	q := r.DB.WithContext(ctx).Preload("Trip").Where("idempotency_key_hash = ?", hash)
	if guest {
		q = q.Where("guest_session_id = ?", ownerID)
	} else {
		q = q.Where("user_id = ? AND guest_session_id IS NULL", ownerID)
	}
	err := q.First(&booking).Error
	return booking, err
}

func (r *Repository) FindBookingForGuest(ctx context.Context, id, guestID uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.WithContext(ctx).Preload("Trip").Preload("Payments").First(&booking, "id = ? AND guest_session_id = ?", id, guestID).Error
	return booking, err
}

// GuestOrderClaim is the factual outcome of a claim attempt. The repository
// reports WHAT the database now holds; the service decides what that means for
// the caller (idempotent replay vs refusal) — policy stays in the service layer.
//
//   - BookingID: the guest order the guest session is anchored to.
//   - OwnerID:   the account that owns that booking AFTER this call. For an
//     already-claimed order this is the first winner, never the current
//     caller, which is what makes a silent transfer impossible.
//   - Transferred: true only when THIS call moved ownership. False means the
//     call was a no-op (the order had already been claimed).
type GuestOrderClaim struct {
	BookingID   uuid.UUID
	OwnerID     uuid.UUID
	Transferred bool
}

// ClaimGuestOrder atomically transfers a guest booking to an authenticated
// account, exactly once, inside a single transaction.
//
// Ownership proof is the guest session row itself (resolved from the HttpOnly
// cookie token hash by the caller): the booking is derived from
// guest_sessions.first_order_id and the UPDATE still requires the booking to
// reference that exact guest session. A booking id supplied from outside, a
// different guest session, or a matching contact email are therefore never
// sufficient — none of them can move a booking.
//
// Sequence and why each step is needed:
//  1. SELECT ... FOR UPDATE on the guest row serializes two simultaneous claim
//     attempts (Postgres re-evaluates the predicate after the wait).
//  2. The claim marker (claimed_user_id) is read BEFORE any write. If it is
//     set, ownership was already decided: return the existing owner and
//     transfer nothing. Two claims can therefore never disagree about who owns
//     the order, no matter who runs second.
//  3. The conditional UPDATE (guest_session_id must still match) performs the
//     transfer and closes the guest path in the same statement.
//  4. The marker UPDATE (claimed_user_id IS NULL) is the second gate; a
//     RowsAffected mismatch aborts the transaction, rolling the transfer back.
//
// Rows whose marker predates these columns (claimed before the migration, or
// filled by an out-of-band owner change) hit step 3 with RowsAffected == 0; the
// current owner is then read from the booking instead of being overwritten.
func (r *Repository) ClaimGuestOrder(ctx context.Context, guestID, userID uuid.UUID) (GuestOrderClaim, error) {
	var claim GuestOrderClaim
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guest models.GuestSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&guest, "id = ? AND expires_at > ?", guestID, time.Now()).Error; err != nil {
			return err
		}
		if guest.FirstOrderID == nil {
			return gorm.ErrRecordNotFound
		}
		bookingID := *guest.FirstOrderID
		if guest.ClaimedUserID != nil {
			claim = GuestOrderClaim{BookingID: bookingID, OwnerID: *guest.ClaimedUserID}
			return nil
		}
		result := tx.Model(&models.Booking{}).Where("id = ? AND guest_session_id = ?", bookingID, guestID).
			Updates(map[string]interface{}{"user_id": userID, "guest_session_id": nil})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return r.resolveUnmarkedGuestOrderClaim(tx, bookingID, &claim)
		}
		now := time.Now()
		marker := tx.Model(&models.GuestSession{}).Where("id = ? AND claimed_user_id IS NULL", guestID).
			Updates(map[string]interface{}{"claimed_user_id": userID, "claimed_at": now})
		if marker.Error != nil {
			return marker.Error
		}
		if marker.RowsAffected != 1 {
			// Marker lost its own race: refuse and roll the transfer back rather
			// than leaving a claimed booking without a recorded claimant.
			return gorm.ErrRecordNotFound
		}
		claim = GuestOrderClaim{BookingID: bookingID, OwnerID: userID, Transferred: true}
		return nil
	})
	if err != nil {
		return GuestOrderClaim{}, err
	}
	return claim, nil
}

// resolveUnmarkedGuestOrderClaim reports the current owner of a guest order that
// the conditional UPDATE could not move while no claim marker was present.
//
// The booking still belonging to a guest session means first_order_id points at
// somebody else's order: the pointer alone is not ownership, so this fails
// closed as "nothing to claim" instead of naming an owner. Otherwise the order
// already left the guest path (a pre-migration claim) and its current user_id is
// the authoritative owner — reported, never overwritten.
func (r *Repository) resolveUnmarkedGuestOrderClaim(tx *gorm.DB, bookingID uuid.UUID, claim *GuestOrderClaim) error {
	var existing models.Booking
	if err := tx.Select("id", "user_id", "guest_session_id").First(&existing, "id = ?", bookingID).Error; err != nil {
		return err
	}
	if existing.GuestSessionID != nil {
		// Still guest-owned while the matching-guest UPDATE affected no row:
		// first_order_id points at a booking of a DIFFERENT guest session.
		return gorm.ErrRecordNotFound
	}
	*claim = GuestOrderClaim{BookingID: bookingID, OwnerID: existing.UserID}
	return nil
}
