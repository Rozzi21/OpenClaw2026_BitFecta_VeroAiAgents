package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
			utils.Error(c, http.StatusForbidden, "Please sign in to create another order.", gin.H{"status": "authentication_required", "code": services.CodeGuestOrderLimitReached})
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

// ClaimOrderToAccount is the explicit retry path for the guest-order claim
// (GO-P1-3). The claim hooks inside Register/Login/GoogleCallback are
// best-effort — they never fail the login — so a claim can be skipped without
// anyone noticing: the classic case is the guest cookie not being sent on the
// cross-site Google callback (SameSite), which leaves the order stranded on a
// guest identity that can never log in.
//
// The proof requirements are exactly the same as the automatic hooks, and no
// weaker:
//   - Bearer access token (route sits behind middlewares.Auth) proves WHICH
//     account claims; a guest with no session gets 401 and claims nothing.
//   - HttpOnly vero_guest_session cookie proves WHICH guest order is at stake.
//     Its SHA-256 digest resolves one guest session row and the booking is read
//     from that row's first_order_id.
//   - The client passes NO order id and NO email: neither is an input here, so
//     neither can select or move an order.
//
// Idempotent: replaying it while already the owner returns 200 with
// transferred=false and writes nothing. An order owned by a DIFFERENT account is
// refused with 409, never transferred.
func (h *Handler) ClaimOrderToAccount(c *gin.Context) {
	userID := currentUserID(c)
	if userID == uuid.Nil {
		// Defense in depth: the route is auth-guarded, but the handler must
		// fail closed on its own so mounting it without Auth cannot turn
		// uuid.Nil into a booking owner.
		utils.Unauthorized(c, "Authentication required")
		return
	}
	result, err := h.Services.Guests.ClaimOrder(c.Request.Context(), auth.GetGuestIdentityCookie(c), userID)
	switch {
	case err == nil:
		utils.Success(c, http.StatusOK, "Order claimed", gin.H{
			"order_id": result.BookingID,
			// false = idempotent replay: this account already owned the order.
			"transferred": result.Transferred,
		})
	case errors.Is(err, services.ErrGuestOrderNothingToClaim):
		// No cookie, unknown/expired guest session, or that session never
		// ordered. Same answer for all three: nothing to reveal.
		utils.Error(c, http.StatusNotFound, "No guest order to claim.", gin.H{"code": "NO_GUEST_ORDER_TO_CLAIM"})
	case errors.Is(err, services.ErrGuestOrderClaimConflict):
		utils.Error(c, http.StatusConflict, "This order already belongs to another account.", gin.H{"code": "GUEST_ORDER_CLAIMED_BY_ANOTHER_ACCOUNT"})
	case errors.Is(err, services.ErrGuestOrderClaimUnauthenticated):
		utils.Unauthorized(c, "Authentication required")
	default:
		utils.ServerError(c, err)
	}
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
