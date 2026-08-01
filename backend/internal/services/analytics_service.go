package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"gorm.io/gorm"
)

// SEC-27: AnalyticsService now depends on the AnalyticsRepository interface
// instead of the concrete *repositories.Repository. This retires the old
// `s.repo.DB` escape hatch (coding-rules §1.1a exception is now closed):
// aggregate SQL lives behind dedicated repository methods and the service can
// be unit-tested with a mock.
type AnalyticsService struct {
	repo repositories.AnalyticsRepository
}

func (s *AnalyticsService) Dashboard(ctx context.Context) (map[string]interface{}, error) {
	totalBookings, err := s.repo.CountBookings(ctx)
	if err != nil {
		return nil, err
	}
	totalRevenue, err := s.repo.SumBookingRevenue(ctx)
	if err != nil {
		return nil, err
	}
	activeTrips, err := s.repo.CountTrips(ctx)
	if err != nil {
		return nil, err
	}
	aiLogs, err := s.repo.CountAILogs(ctx)
	if err != nil {
		return nil, err
	}
	allPayments, err := s.repo.CountPayments(ctx)
	if err != nil {
		return nil, err
	}
	// SEC-29: success statuses live in models.PaymentSuccessStatuses() and are
	// applied inside the repository method (no raw string slice in the service).
	paidPayments, err := s.repo.CountSuccessfulPayments(ctx)
	if err != nil {
		return nil, err
	}

	successRate := 0.0
	if allPayments > 0 {
		successRate = float64(paidPayments) / float64(allPayments) * 100
	}

	// Use RecentBookings (limited, no payments preload) instead of ListBookings
	// to avoid loading the entire bookings table + 3 preloads on every dashboard load.
	recentBookings, err := s.repo.RecentBookings(ctx, 10)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	return map[string]interface{}{

		"total_bookings":       totalBookings,
		"total_revenue":        totalRevenue,
		"active_trips":         activeTrips,
		"ai_usage_stats":       aiLogs,
		"payment_success_rate": fmt.Sprintf("%.2f%%", successRate),
		"customer_activity":    recentBookings,
	}, nil
}
