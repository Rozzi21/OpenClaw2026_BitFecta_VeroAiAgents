package repositories

import (
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

func (r *Repository) FindBookingBySession(sessionID uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.Order("created_at desc").First(&booking, "session_id = ?", sessionID).Error
	return booking, err
}

func (r *Repository) CreateBooking(booking *models.Booking) error {
	return r.DB.Create(booking).Error
}

func (r *Repository) ListBookings(query RepositoryFilter) ([]models.Booking, error) {
	var bookings []models.Booking
	err := r.DB.Preload("User").Preload("Trip").Preload("Payments").
		Order("created_at desc").Limit(query.Limit).Offset(query.Offset).Find(&bookings).Error
	return bookings, err
}

// RecentBookings returns the most recent bookings (without payments preload) for
// analytics dashboards. This avoids the full-table scan + 3-preload pattern that
// ListBookings uses, keeping dashboard queries lightweight.
func (r *Repository) RecentBookings(limit int) ([]models.Booking, error) {
	if limit <= 0 {
		limit = 10
	}
	var bookings []models.Booking
	err := r.DB.Preload("User").Preload("Trip").
		Order("created_at desc").Limit(limit).Find(&bookings).Error
	return bookings, err
}

func (r *Repository) FindBooking(id uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.Preload("User").Preload("Trip").Preload("Payments").First(&booking, "id = ?", id).Error
	return booking, err
}

// FindBookingForUser scopes the lookup to a single owner (SEC-2 anti-IDOR).
// Staff callers should use FindBooking instead.
func (r *Repository) FindBookingForUser(id, userID uuid.UUID) (models.Booking, error) {
	var booking models.Booking
	err := r.DB.Preload("User").Preload("Trip").Preload("Payments").
		First(&booking, "id = ? AND user_id = ?", id, userID).Error
	return booking, err
}

func (r *Repository) UpdateBooking(booking *models.Booking) error {
	return r.DB.Save(booking).Error
}

// UpdateBookingStatusAtomic performs an atomic conditional update of the booking
// status. It only succeeds if the current DB status matches fromStatus, preventing
// TOCTOU race conditions (SEC-23). Returns true if the row was updated (race won),
// false if the status had already changed (race lost).
func (r *Repository) UpdateBookingStatusAtomic(id uuid.UUID, fromStatus, toStatus string) (bool, error) {
	result := r.DB.Model(&models.Booking{}).
		Where("id = ? AND booking_status = ?", id, fromStatus).
		Update("booking_status", toStatus)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}
