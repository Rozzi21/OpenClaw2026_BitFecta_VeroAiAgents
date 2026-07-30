package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

func (h *Handler) ListTrips(c *gin.Context) {
	var query dto.TripListQuery
	_ = c.ShouldBindQuery(&query)
	trips, err := h.Services.Trips.List(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Trips", trips)
}

func (h *Handler) PublicPackages(c *gin.Context) {
	var query dto.TripListQuery
	_ = c.ShouldBindQuery(&query)
	query.PublishedOnly = true
	trips, err := h.Services.Trips.List(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Packages", trips)
}

func (h *Handler) GetPackage(c *gin.Context) {
	trip, err := h.Services.Trips.FindBySlugOrID(c.Param("id"))
	if err != nil || trip.Status != "published" {
		utils.NotFound(c, "Package not found")
		return
	}
	utils.Success(c, http.StatusOK, "Package", trip)
}

func (h *Handler) GetTrip(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	trip, err := h.Services.Trips.Find(id)
	if err != nil {
		utils.NotFound(c, "Trip not found")
		return
	}
	utils.Success(c, http.StatusOK, "Trip", trip)
}

func (h *Handler) CreateTrip(c *gin.Context) {
	var req dto.TripRequest
	if !bind(c, &req) {
		return
	}
	trip, err := h.Services.Trips.Create(req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Trip created", trip)
}

func (h *Handler) UpdateTrip(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var req dto.TripRequest
	if !bind(c, &req) {
		return
	}
	trip, err := h.Services.Trips.Update(id, req)
	if err != nil {
		utils.NotFound(c, "Trip not found")
		return
	}
	utils.Success(c, http.StatusOK, "Trip updated", trip)
}

func (h *Handler) DeleteTrip(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	if err := h.Services.Trips.Delete(id); err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Trip deleted", gin.H{"id": id})
}
