package repositories

import (
	"context"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// SEC-27: Aggregate query methods for the analytics dashboard. These replace
// the AnalyticsService `s.repo.DB` escape hatch (coding-rules §1.1a exception
// is now closed) so the service depends only on the AnalyticsRepository
// interface and can be unit-tested with a mock. Aggregate SQL stays inside the
// repository layer where it belongs.

func (r *Repository) CountBookings(ctx context.Context) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&models.Booking{}).Count(&count).Error
	return count, err
}

func (r *Repository) SumBookingRevenue(ctx context.Context) (float64, error) {
	var revenue float64
	err := r.DB.WithContext(ctx).Model(&models.Booking{}).
		Select("COALESCE(SUM(total_price), 0)").Scan(&revenue).Error
	return revenue, err
}

func (r *Repository) CountTrips(ctx context.Context) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&models.Trip{}).Count(&count).Error
	return count, err
}

func (r *Repository) CountAILogs(ctx context.Context) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&models.AILog{}).Count(&count).Error
	return count, err
}

func (r *Repository) CountPayments(ctx context.Context) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&models.Payment{}).Count(&count).Error
	return count, err
}

// CountSuccessfulPayments counts payments whose status is in the canonical
// success set (SEC-29: success statuses live in models.PaymentSuccessStatuses).
func (r *Repository) CountSuccessfulPayments(ctx context.Context) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&models.Payment{}).
		Where("status IN ?", models.PaymentSuccessStatuses()).Count(&count).Error
	return count, err
}
