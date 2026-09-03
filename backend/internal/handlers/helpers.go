package handlers

import (
	"errors"
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/middlewares"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

func authRequestMeta(c *gin.Context) services.AuthRequestMeta {
	requestID, _ := c.Get("request_id")
	id, _ := requestID.(string)
	return services.AuthRequestMeta{
		IP:        c.ClientIP(),
		UserAgent: c.GetHeader("User-Agent"),
		RequestID: id,
	}
}

func respondAuthIssue(c *gin.Context, cfg config.Config, status int, message string, result services.AuthIssueResult) {
	maxAge := int(cfg.JWTRefreshTTL.Seconds())
	auth.SetRefreshCookie(c, cfg, result.RefreshToken, maxAge)
	utils.Success(c, status, message, result.Response)
}

// claimGuestOrder moves a pending guest order to the account that just
// authenticated (password login/register and the Google OAuth callback all use
// this one path).
//
// Never fatal to the auth response, by design: no guest cookie is the normal
// case, and a refusal must not break the login it was piggybacking on. What
// changed with GO-P1-3 is that each outcome is now distinguishable instead of
// collapsing into one "claim failed" log line — a real failure and a
// wrong-account refusal are separate, greppable events.
func (h *Handler) claimGuestOrder(c *gin.Context, userID uuid.UUID, flow string) {
	result, err := h.Services.Guests.ClaimOrder(c.Request.Context(), auth.GetGuestIdentityCookie(c), userID)
	switch {
	case err == nil:
		if result.Transferred {
			log.Printf("[%s] guest order %s claimed by user=%s", flow, result.BookingID, userID)
		}
	case errors.Is(err, services.ErrGuestOrderNothingToClaim):
		// Nothing pending for this browser — the common path, stays quiet.
	case errors.Is(err, services.ErrGuestOrderClaimConflict):
		log.Printf("[%s] guest order claim refused, order owned by another account: user=%s", flow, userID)
	default:
		log.Printf("[%s] guest order claim failed user=%s: %v", flow, userID, err)
	}
}

func bind(c *gin.Context, target interface{}) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		utils.BadRequest(c, "Validation failed", gin.H{"detail": err.Error()})
		return false
	}
	return true
}

func parseID(c *gin.Context, key string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(key))
	if err != nil {
		utils.BadRequest(c, fmt.Sprintf("Invalid %s", key), gin.H{"detail": err.Error()})
		return uuid.Nil, false
	}
	return id, true
}

func currentUserID(c *gin.Context) uuid.UUID {
	value, exists := c.Get(middlewares.ContextUserID)
	if !exists {
		return uuid.Nil
	}
	id, ok := value.(uuid.UUID)
	if !ok {
		return uuid.Nil
	}
	return id
}

// isStaff reports whether the current request belongs to an operator/admin.
// Used for ownership checks: staff may read any resource, others only their own.
func isStaff(c *gin.Context) bool {
	value, exists := c.Get(middlewares.ContextRole)
	if !exists {
		return false
	}
	role, ok := value.(models.Role)
	if !ok {
		return false
	}
	return role == models.RoleOperator || role == models.RoleAdmin
}
