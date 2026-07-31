package repositories

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"gorm.io/gorm"
)

func (r *Repository) CreateTrip(ctx context.Context, trip *models.Trip) error {
	return r.DB.WithContext(ctx).Create(trip).Error
}

func (r *Repository) ListTrips(ctx context.Context, query TripRepositoryFilter) ([]models.Trip, error) {
	var trips []models.Trip
	db := r.DB.WithContext(ctx).Preload("Itineraries").Order("created_at desc")
	if query.Category != "" {
		db = db.Where("category = ?", strings.ToLower(query.Category))
	}
	if query.Status != "" {
		db = db.Where("status = ?", strings.ToLower(query.Status))
	}
	if query.PublishedOnly {
		db = db.Where("status = ?", "published")
	}
	if query.Search != "" {
		like := "%" + strings.ToLower(query.Search) + "%"
		db = db.Where("LOWER(title) LIKE ? OR LOWER(destination) LIKE ? OR LOWER(location) LIKE ?", like, like, like)
	}
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}
	if query.Offset > 0 {
		db = db.Offset(query.Offset)
	}
	err := db.Find(&trips).Error
	return trips, err
}

func (r *Repository) FindTrip(ctx context.Context, id uuid.UUID) (models.Trip, error) {
	var trip models.Trip
	err := r.DB.WithContext(ctx).Preload("Itineraries").First(&trip, "id = ?", id).Error
	return trip, err
}

func (r *Repository) FindTripBySlugOrID(ctx context.Context, value string) (models.Trip, error) {
	var trip models.Trip
	if id, err := uuid.Parse(value); err == nil {
		err = r.DB.WithContext(ctx).Preload("Itineraries").First(&trip, "id = ?", id).Error
		return trip, err
	}
	err := r.DB.WithContext(ctx).Preload("Itineraries").First(&trip, "slug = ?", value).Error
	return trip, err
}

func (r *Repository) UpdateTrip(ctx context.Context, trip *models.Trip) error {
	return r.DB.WithContext(ctx).Save(trip).Error
}

func (r *Repository) ReplaceTripItineraries(ctx context.Context, tripID uuid.UUID, itineraries []models.Itinerary) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("trip_id = ?", tripID).Delete(&models.Itinerary{}).Error; err != nil {
			return err
		}
		if len(itineraries) == 0 {
			return nil
		}
		for i := range itineraries {
			itineraries[i].TripID = tripID
		}
		return tx.Create(&itineraries).Error
	})
}

func (r *Repository) DeleteTrip(ctx context.Context, id uuid.UUID) error {
	return r.DB.WithContext(ctx).Delete(&models.Trip{}, "id = ?", id).Error
}
