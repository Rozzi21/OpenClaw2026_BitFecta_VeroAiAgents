package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

func (r *Repository) CreatePayment(ctx context.Context, payment *models.Payment) error {
	return r.DB.WithContext(ctx).Create(payment).Error
}

func (r *Repository) FindPayment(ctx context.Context, id uuid.UUID) (models.Payment, error) {
	var payment models.Payment
	err := r.DB.WithContext(ctx).Preload("Booking").First(&payment, "id = ?", id).Error
	return payment, err
}

// FindPaymentForUser scopes the lookup to the owner of the related booking
// (SEC-2 anti-IDOR). Staff callers should use FindPayment instead.
func (r *Repository) FindPaymentForUser(ctx context.Context, id, userID uuid.UUID) (models.Payment, error) {
	var payment models.Payment
	err := r.DB.WithContext(ctx).Preload("Booking").
		Joins("JOIN bookings ON bookings.id = payments.booking_id").
		Where("payments.id = ? AND bookings.user_id = ?", id, userID).
		First(&payment).Error
	return payment, err
}

func (r *Repository) FindPaymentByExternalID(ctx context.Context, externalID string) (models.Payment, error) {
	var payment models.Payment
	err := r.DB.WithContext(ctx).Preload("Booking").First(&payment, "external_id = ?", externalID).Error
	return payment, err
}

func (r *Repository) UpdatePayment(ctx context.Context, payment *models.Payment) error {
	return r.DB.WithContext(ctx).Save(payment).Error
}

// UpdatePaymentStatusAtomic applies a conditional status transition in a
// single UPDATE guarded by the expected current status (SEC-29). Returns
// updated=true when the row was transitioned; false when another writer
// already moved it (caller should re-read and decide). Mirrors
// UpdateBookingStatusAtomic (SEC-23).
func (r *Repository) UpdatePaymentStatusAtomic(ctx context.Context, id uuid.UUID, fromStatus, toStatus string) (bool, error) {
	res := r.DB.WithContext(ctx).Model(&models.Payment{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}
