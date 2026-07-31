package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

func (h *Handler) CreatePayment(c *gin.Context) {
	if !h.Services.Config.PaymentsEnabled {
		h.PaymentFeatureDisabled(c)
		return
	}
	var req dto.PaymentCreateRequest
	if !bind(c, &req) {
		return
	}
	payment, err := h.Services.Payments.Create(c.Request.Context(), req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Payment created", payment)
}

func (h *Handler) PaymentWebhook(c *gin.Context) {
	if !h.Services.Config.PaymentsEnabled {
		h.PaymentFeatureDisabled(c)
		return
	}
	var req dto.PaymentWebhookRequest
	if !bind(c, &req) {
		return
	}
	if req.Signature == "" {
		req.Signature = c.GetHeader("X-Doku-Signature")
	}
	if req.Timestamp == "" {
		req.Timestamp = c.GetHeader("X-Doku-Timestamp")
	}

	rawBody, err := c.GetRawData()
	if err == nil {
		req.RawBody = rawBody
	}

	payment, err := h.Services.Payments.Webhook(c.Request.Context(), req)
	if err != nil {
		// SEC-15: do not echo internal/payment errors to an unauthenticated
		// caller; log server-side.
		log.Printf("[payment-webhook] rejected: %v", err)
		utils.BadRequest(c, "Webhook failed", gin.H{})
		return
	}
	utils.Success(c, http.StatusOK, "Payment updated", payment)
}

func (h *Handler) GetPayment(c *gin.Context) {
	if !h.Services.Config.PaymentsEnabled {
		h.PaymentFeatureDisabled(c)
		return
	}
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	payment, err := h.Services.Payments.Find(c.Request.Context(), id, currentUserID(c), isStaff(c))
	if err != nil {
		utils.NotFound(c, "Payment not found")
		return
	}
	utils.Success(c, http.StatusOK, "Payment", payment)
}

func (h *Handler) PaymentFeatureDisabled(c *gin.Context) {
	utils.Error(c, http.StatusServiceUnavailable, "Payment feature temporarily disabled", gin.H{
		"detail": "DOKU payment flow is disabled; orders are saved as pending for manual backoffice processing.",
	})
}
