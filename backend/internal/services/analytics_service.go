package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"gorm.io/gorm"
)

type AnalyticsService struct{ repo *repositories.Repository }

func (s *AnalyticsService) Dashboard(ctx context.Context) (map[string]interface{}, error) {
	var totalBookings int64
	var totalRevenue float64
	var activeTrips int64
	var aiLogs int64
	var paidPayments int64
	var allPayments int64

	db := s.repo.DB.WithContext(ctx)
	db.Model(&models.Booking{}).Count(&totalBookings)
	db.Model(&models.Booking{}).Select("COALESCE(SUM(total_price), 0)").Scan(&totalRevenue)
	db.Model(&models.Trip{}).Count(&activeTrips)
	db.Model(&models.AILog{}).Count(&aiLogs)
	db.Model(&models.Payment{}).Count(&allPayments)
	// SEC-29: success statuses live in models.PaymentSuccessStatuses() instead
	// of a re-declared raw string slice.
	db.Model(&models.Payment{}).Where("status IN ?", models.PaymentSuccessStatuses()).Count(&paidPayments)

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
