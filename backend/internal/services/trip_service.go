package services

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

type TripService struct {
	repo *repositories.Repository
	bus  *events.Bus
}

func (s *TripService) List(query dto.TripListQuery) ([]models.Trip, error) {
	return s.repo.ListTrips(query)
}
func (s *TripService) Find(id uuid.UUID) (models.Trip, error) { return s.repo.FindTrip(id) }
func (s *TripService) FindBySlugOrID(value string) (models.Trip, error) {
	return s.repo.FindTripBySlugOrID(value)
}
func (s *TripService) Create(req dto.TripRequest) (models.Trip, error) {
	trip := buildTripFromRequest(models.Trip{}, req)
	if trip.Slug == "" {
		trip.Slug = slugify(trip.Title)
	}
	if trip.Status == "published" {
		now := time.Now()
		trip.PublishedAt = &now
	}
	err := s.repo.CreateTrip(&trip)
	if err == nil && len(req.Itineraries) > 0 {
		err = s.repo.ReplaceTripItineraries(trip.ID, buildItineraries(req.Itineraries))
		if err == nil {
			trip, _ = s.repo.FindTrip(trip.ID)
		}
	}
	if err == nil {
		s.bus.Publish("trip_created", trip)
	}
	return trip, err
}
func (s *TripService) Update(id uuid.UUID, req dto.TripRequest) (models.Trip, error) {
	trip, err := s.repo.FindTrip(id)
	if err != nil {
		return trip, err
	}
	trip = buildTripFromRequest(trip, req)
	if trip.Slug == "" {
		trip.Slug = slugify(trip.Title)
	}
	if trip.Status == "published" && trip.PublishedAt == nil {
		now := time.Now()
		trip.PublishedAt = &now
	}
	err = s.repo.UpdateTrip(&trip)
	if err == nil {
		err = s.repo.ReplaceTripItineraries(trip.ID, buildItineraries(req.Itineraries))
		if err == nil {
			trip, _ = s.repo.FindTrip(trip.ID)
		}
	}
	return trip, err
}
func (s *TripService) Delete(id uuid.UUID) error { return s.repo.DeleteTrip(id) }

func buildTripFromRequest(trip models.Trip, req dto.TripRequest) models.Trip {
	// BUG-7: Clamp invalid price values from bypassers.
	if req.BasePrice < 0 {
		req.BasePrice = 0
	}
	if req.EstimatedPrice < 0 {
		req.EstimatedPrice = 0
	}
	if req.DiscountPrice < 0 {
		req.DiscountPrice = 0
	}
	if req.ChildPrice < 0 {
		req.ChildPrice = 0
	}
	if req.ChildDiscountPrice < 0 {
		req.ChildDiscountPrice = 0
	}
	if req.BasePrice > 999999999999 {
		req.BasePrice = 999999999999
	}
	if req.EstimatedPrice > 999999999999 {
		req.EstimatedPrice = 999999999999
	}
	if req.DiscountPrice > 999999999999 {
		req.DiscountPrice = 999999999999
	}
	if req.ChildPrice > 999999999999 {
		req.ChildPrice = 999999999999
	}
	if req.ChildDiscountPrice > 999999999999 {
		req.ChildDiscountPrice = 999999999999
	}

	trip.Title = req.Title
	trip.Slug = req.Slug
	trip.Destination = firstNonEmpty(req.Destination, req.Location)
	trip.Location = firstNonEmpty(req.Location, req.Destination)
	trip.Category = normalize(req.Category, "international")
	trip.Status = normalize(req.Status, "draft")
	trip.Overview = firstNonEmpty(req.Overview, req.Summary)
	trip.Summary = firstNonEmpty(req.Summary, req.Overview)
	trip.Duration = req.Duration
	trip.AdultPax = req.AdultPax
	trip.ChildPax = req.ChildPax
	trip.EstimatedPrice = firstNonZero(req.EstimatedPrice, req.BasePrice)
	trip.BasePrice = firstNonZero(req.BasePrice, req.EstimatedPrice)
	trip.DiscountPrice = req.DiscountPrice
	trip.ChildPrice = req.ChildPrice
	trip.ChildDiscount = req.ChildDiscountPrice
	trip.DiscountEnabled = req.DiscountEnabled
	trip.ChildDiscountEnabled = req.ChildDiscountEnabled
	trip.ImageURL = req.ImageURL
	trip.Media = make([]models.TripMedia, 0, len(req.Media))
	for _, media := range req.Media {
		if media.URL == "" {
			continue
		}
		trip.Media = append(trip.Media, models.TripMedia{URL: media.URL, Type: firstNonEmpty(media.Type, "image"), AltText: media.AltText})
		if trip.ImageURL == "" {
			trip.ImageURL = media.URL
		}
	}
	trip.Highlights = req.Highlights
	trip.AmenitiesIncluded = req.AmenitiesIncluded
	trip.AmenitiesExcluded = req.AmenitiesExcluded
	trip.References = req.References
	trip.ScheduleType = firstNonEmpty(req.ScheduleType, "date_range")
	trip.PackageStartDate = parseDate(req.PackageStartDate)
	trip.PackageEndDate = parseDate(req.PackageEndDate)
	trip.PublishStartDate = parseDate(req.PublishStartDate)
	trip.PublishEndDate = parseDate(req.PublishEndDate)
	return trip
}

func buildItineraries(items []dto.ItineraryRequest) []models.Itinerary {
	itineraries := make([]models.Itinerary, 0, len(items))
	for index, item := range items {
		day := item.Day
		if day <= 0 {
			day = index + 1
		}
		if item.Title == "" && item.Description == "" {
			continue
		}
		itineraries = append(itineraries, models.Itinerary{
			Day:         day,
			Title:       firstNonEmpty(item.Title, fmt.Sprintf("Day %d", day)),
			Description: item.Description,
		})
	}
	return itineraries
}
