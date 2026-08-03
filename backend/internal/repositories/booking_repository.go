package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

func (r *Repository) FindBookingBySession(ctx context.Context, sessionID uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.WithContext(ctx).Order("created_at desc").First(&booking, "session_id = ?", sessionID).Error
	return booking, err
}

func (r *Repository) CreateBooking(ctx context.Context, booking *models.Booking) error {
	return r.DB.WithContext(ctx).Create(booking).Error
}

func (r *Repository) ListBookings(ctx context.Context, query RepositoryFilter) ([]models.Booking, error) {
	var bookings []models.Booking
	err := r.DB.WithContext(ctx).Preload("User").Preload("Trip").Preload("Payments").
		Order("created_at desc").Limit(query.Limit).Offset(query.Offset).Find(&bookings).Error
	return bookings, err
}

// RecentBookings returns the most recent bookings (without payments preload) for
// analytics dashboards. This avoids the full-table scan + 3-preload pattern that
// ListBookings uses, keeping dashboard queries lightweight.
func (r *Repository) RecentBookings(ctx context.Context, limit int) ([]models.Booking, error) {
	if limit <= 0 {
		limit = 10
	}
	var bookings []models.Booking
	err := r.DB.WithContext(ctx).Preload("User").Preload("Trip").
		Order("created_at desc").Limit(limit).Find(&bookings).Error
	return bookings, err
}

func (r *Repository) FindBooking(ctx context.Context, id uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.WithContext(ctx).Preload("User").Preload("Trip").Preload("Payments").First(&booking, "id = ?", id).Error
	return booking, err
}

// FindBookingForUser scopes the lookup to a single owner (SEC-2 anti-IDOR).
// Staff callers should use FindBooking instead.
func (r *Repository) FindBookingForUser(ctx context.Context, id, userID uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.WithContext(ctx).Preload("User").Preload("Trip").Preload("Payments").
		First(&booking, "id = ? AND user_id = ?", id, userID).Error
	return booking, err
}

// UpdateBooking persists the booking's editable columns without touching its
// associations (DB-2). The previous .Save() full-overwrote every column from
// the in-memory struct AND upserted preloaded associations (User/Trip/Payments
// — FindBooking preloads all three) when invoked on a fetched record, risking
// lost updates and association clobber (e.g. clobbering Payment rows).
// .Select("*").Updates() writes only model columns, leaving associations and
// preloaded slices untouched.
//
// NOTE: status transitions must NOT use this method — they go through
// UpdateBookingStatusAtomic (SEC-23) for TOCTOU-safe conditional updates.
// This method has no current callers but is retained on the interface for
// future non-status full edits (e.g. contact info correction); keep the
// association-safe form so a latent caller cannot reintroduce DB-2.
func (r *Repository) UpdateBooking(ctx context.Context, booking *models.Booking) error {
	return r.DB.WithContext(ctx).Model(&models.Booking{}).
		Where("id = ?", booking.ID).
		Select("*").
		Updates(booking).Error
}

// UpdateBookingStatusAtomic performs an atomic conditional update of the booking
// status. It only succeeds if the current DB status matches fromStatus, preventing
// TOCTOU race conditions (SEC-23). Returns true if the row was updated (race won),
// false if the status had already changed (race lost).
func (r *Repository) UpdateBookingStatusAtomic(ctx context.Context, id uuid.UUID, fromStatus, toStatus string) (bool, error) {
	result := r.DB.WithContext(ctx).Model(&models.Booking{}).
		Where("id = ? AND booking_status = ?", id, fromStatus).
		Update("booking_status", toStatus)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}
