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

func (r *Repository) UpdateChatSessionGuest(ctx context.Context, chatID, guestID uuid.UUID) error {
	return r.DB.WithContext(ctx).Model(&models.ChatSession{}).Where("id = ?", chatID).Update("guest_session_id", guestID).Error
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

// ClaimGuestOrder atomically transfers a guest booking to an authenticated
// account. The booking must still reference the exact guest session and may
// only be claimed once (guest_session_id becomes NULL in the same statement).
// Locking the guest row serializes two simultaneous claim attempts; the
// conditional UPDATE (RowsAffected==1) then decides the winner, so a lost race
// surfaces as record-not-found rather than double-linking.
func (r *Repository) ClaimGuestOrder(ctx context.Context, guestID, userID uuid.UUID) (uuid.UUID, error) {
	var bookingID uuid.UUID
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guest models.GuestSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&guest, "id = ? AND expires_at > ?", guestID, time.Now()).Error; err != nil {
			return err
		}
		if guest.FirstOrderID == nil {
			return gorm.ErrRecordNotFound
		}
		result := tx.Model(&models.Booking{}).Where("id = ? AND guest_session_id = ?", *guest.FirstOrderID, guestID).
			Updates(map[string]interface{}{"user_id": userID, "guest_session_id": nil})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		bookingID = *guest.FirstOrderID
		return nil
	})
	return bookingID, err
}
