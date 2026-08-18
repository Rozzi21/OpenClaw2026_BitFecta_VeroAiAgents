package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

func (h *Handler) CreateBooking(c *gin.Context) {
	var req dto.BookingRequest
	if !bind(c, &req) {
		return
	}
	ctx := c.Request.Context()
	booking, err := h.Services.Bookings.Create(ctx, currentUserID(c), c.GetHeader("Idempotency-Key"), req)
	if err != nil {
		if errors.Is(err, services.ErrIdempotencyKeyRequired) {
			utils.BadRequest(c, "A valid Idempotency-Key header is required", gin.H{"code": "IDEMPOTENCY_KEY_REQUIRED"})
			return
		}
		if errors.Is(err, services.ErrBookingContactRequired) || errors.Is(err, services.ErrBookingTravelDateInvalid) {
			utils.BadRequest(c, "Booking validation failed", gin.H{"code": "BOOKING_VALIDATION_FAILED"})
			return
		}
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
	ctx := c.Request.Context()
	identity, err := h.Services.Guests.Resolve(ctx, auth.GetGuestIdentityCookie(c))
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	auth.SetGuestIdentityCookie(c, h.Services.Config, identity.Token, int(h.Services.Config.GuestIdentityTTL.Seconds()))
	booking, err := h.Services.Bookings.CreateGuest(ctx, identity.Session.UserID, identity.Session.ID, c.GetHeader("Idempotency-Key"), req)
	if err != nil {
		if errors.Is(err, services.ErrGuestOrderLimitReached) {
			utils.Error(c, http.StatusForbidden, "Please sign in to create another order.", gin.H{"status": "authentication_required", "code": "GUEST_ORDER_LIMIT_REACHED"})
			return
		}
		if errors.Is(err, services.ErrIdempotencyKeyRequired) {
			utils.BadRequest(c, "A valid Idempotency-Key header is required", gin.H{"code": "IDEMPOTENCY_KEY_REQUIRED"})
			return
		}
		if errors.Is(err, services.ErrBookingContactRequired) || errors.Is(err, services.ErrBookingTravelDateInvalid) {
			utils.BadRequest(c, "Booking validation failed", gin.H{"code": "BOOKING_VALIDATION_FAILED"})
			return
		}
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Order created for manual admin processing", booking)
}

func (h *Handler) GuestGetOrder(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	guest, err := h.Services.Guests.Authenticate(c.Request.Context(), auth.GetGuestIdentityCookie(c))
	if err != nil {
		utils.NotFound(c, "Order not found")
		return
	}
	booking, err := h.Services.Bookings.FindGuest(c.Request.Context(), id, guest.ID)
	if err != nil {
		utils.NotFound(c, "Order not found")
		return
	}
	utils.Success(c, http.StatusOK, "Order", booking)
}

func (h *Handler) ListBookings(c *gin.Context) {
	var query dto.ListQuery
	_ = c.ShouldBindQuery(&query)
	query.Normalize()
	bookings, err := h.Services.Bookings.List(c.Request.Context(), query)
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
	booking, err := h.Services.Bookings.Find(c.Request.Context(), id, currentUserID(c), isStaff(c))
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
	booking, err := h.Services.Bookings.UpdateStatus(c.Request.Context(), id, currentUserID(c), isStaff(c), req)
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
