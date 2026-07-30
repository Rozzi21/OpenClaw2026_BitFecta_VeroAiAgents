package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

func (h *Handler) CreateBooking(c *gin.Context) {
	var req dto.BookingRequest
	if !bind(c, &req) {
		return
	}
	booking, err := h.Services.Bookings.Create(currentUserID(c), req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Booking created", booking)
}

func (h *Handler) GuestCreateOrder(c *gin.Context) {
	var req dto.BookingRequest
	if !bind(c, &req) {
		return
	}
	user, err := h.Services.Auth.GuestUser()
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	booking, err := h.Services.Bookings.Create(user.ID, req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Order created for manual admin processing", booking)
}

func (h *Handler) ListBookings(c *gin.Context) {
	var query dto.ListQuery
	_ = c.ShouldBindQuery(&query)
	query.Normalize()
	bookings, err := h.Services.Bookings.List(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Bookings", bookings)
}

func (h *Handler) GetBooking(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	booking, err := h.Services.Bookings.Find(id, currentUserID(c), isStaff(c))
	if err != nil {
		utils.NotFound(c, "Booking not found")
		return
	}
	utils.Success(c, http.StatusOK, "Booking", booking)
}

func (h *Handler) UpdateBooking(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateBookingStatusRequest
	if !bind(c, &req) {
		return
	}
	booking, err := h.Services.Bookings.UpdateStatus(id, currentUserID(c), isStaff(c), req)
	if err != nil {
		if errors.Is(err, services.ErrBookingNotFound) {
			utils.NotFound(c, "Booking not found")
			return
		}
		utils.BadRequest(c, "Update booking failed", gin.H{})
		return
	}
	utils.Success(c, http.StatusOK, "Booking updated", booking)
}
