package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

func (h *Handler) Register(c *gin.Context) {
	var req dto.RegisterRequest
	if !bind(c, &req) {
		return
	}
	result, err := h.Services.Auth.Register(c.Request.Context(), req, authRequestMeta(c))
	if err != nil {
		// SEC-15: hide raw service/DB errors (e.g. duplicate-email constraint)
		// from the client; log server-side.
		log.Printf("[register] failed: %v", err)
		utils.BadRequest(c, "Registration failed", gin.H{})
		return
	}
	if user, ok := result.Response.User.(models.User); ok {
		h.claimGuestOrder(c, user.ID, "register")
	}
	respondAuthIssue(c, h.Services.Config, http.StatusCreated, "Registered", result)
}

func (h *Handler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if !bind(c, &req) {
		return
	}
	result, err := h.Services.Auth.Login(c.Request.Context(), req, authRequestMeta(c))
	if err != nil {
		utils.Unauthorized(c, err.Error())
		return
	}
	if user, ok := result.Response.User.(models.User); ok {
		h.claimGuestOrder(c, user.ID, "login")
	}
	respondAuthIssue(c, h.Services.Config, http.StatusOK, "Logged in", result)
}

func (h *Handler) Refresh(c *gin.Context) {
	refreshToken := auth.GetRefreshCookie(c, h.Services.Config)
	result, err := h.Services.Auth.Refresh(c.Request.Context(), refreshToken, authRequestMeta(c))
	if err != nil {
		message := "Invalid refresh token"
		if errors.Is(err, services.ErrRefreshTokenRevoked) {
			message = "Refresh token revoked"
		}
		utils.Unauthorized(c, message)
		return
	}
	result.Response.User = nil
	respondAuthIssue(c, h.Services.Config, http.StatusOK, "Token refreshed", result)
}

func (h *Handler) Logout(c *gin.Context) {
	refreshToken := auth.GetRefreshCookie(c, h.Services.Config)
	_ = h.Services.Auth.Logout(c.Request.Context(), refreshToken, authRequestMeta(c))
	auth.ClearRefreshCookie(c, h.Services.Config)
	utils.Success(c, http.StatusOK, "Logged out", gin.H{})
}

func (h *Handler) Me(c *gin.Context) {
	user, err := h.Services.Auth.Me(c.Request.Context(), currentUserID(c))
	if err != nil {
		utils.NotFound(c, "User not found")
		return
	}
	utils.Success(c, http.StatusOK, "Current user", user)
}

// AdminCreateUser provisions operator/admin accounts. Guarded by Role(admin).
func (h *Handler) AdminCreateUser(c *gin.Context) {
	var req dto.AdminCreateUserRequest
	if !bind(c, &req) {
		return
	}
	user, err := h.Services.Auth.CreateStaff(c.Request.Context(), req, authRequestMeta(c))
	if err != nil {
		log.Printf("[admin-create-user] failed: %v", err)
		utils.BadRequest(c, "Create user failed", gin.H{})
		return
	}
	utils.Success(c, http.StatusCreated, "User created", user)
}
