package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

func (h *Handler) Chat(c *gin.Context) {
	var req dto.ChatRequest
	if !bind(c, &req) {
		return
	}
	if req.SessionID == nil {
		utils.BadRequest(c, "session_id is required", gin.H{})
		return
	}
	userID := currentUserID(c)
	res, err := h.Services.AI.Chat(c.Request.Context(), services.ChatContext{SessionID: *req.SessionID, UserID: &userID}, req)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "AI workflow completed", res)
}

func (h *Handler) GuestChat(c *gin.Context) {
	var req dto.ChatRequest
	if !bind(c, &req) {
		return
	}
	sessionID, err := resolveGuestSession(h, c)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	res, err := h.Services.AI.Chat(c.Request.Context(), services.ChatContext{SessionID: sessionID}, req)
	if err != nil {
		if errors.Is(err, services.ErrChatSessionExpired) || errors.Is(err, services.ErrChatSessionNotFound) {
			auth.ClearGuestSessionCookie(c, h.Services.Config)
			utils.BadRequest(c, "Chat session expired", gin.H{})
			return
		}
		utils.ServerError(c, err)
		return
	}
	auth.SetGuestSessionCookie(c, h.Services.Config, sessionID.String(), int(h.Services.Config.GuestSessionTTL.Seconds()))
	utils.Success(c, http.StatusOK, "AI workflow completed", res)
}

func (h *Handler) ChatSessions(c *gin.Context) {
	sessions, err := h.Services.AI.ListSessions(c.Request.Context(), currentUserID(c))
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Chat sessions", sessions)
}

func (h *Handler) ChatMessages(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	messages, err := h.Services.AI.GetSessionMessages(c.Request.Context(), id, currentUserID(c))
	if err != nil {
		if errors.Is(err, services.ErrChatSessionNotFound) || errors.Is(err, services.ErrChatSessionExpired) {
			utils.NotFound(c, "Chat session not found")
			return
		}
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Chat messages", messages)
}

// GuestHistory restores the current anonymous session without accepting an
// identifier from the request. The cookie is the ownership proof.
func (h *Handler) GuestHistory(c *gin.Context) {
	id, err := uuid.Parse(auth.GetGuestSessionCookie(c))
	if err != nil {
		utils.Success(c, http.StatusOK, "Chat history", gin.H{"messages": []models.ChatMessage{}})
		return
	}
	messages, err := h.Services.AI.GetGuestHistory(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrChatSessionNotFound) || errors.Is(err, services.ErrChatSessionExpired) {
			auth.ClearGuestSessionCookie(c, h.Services.Config)
			utils.Success(c, http.StatusOK, "Chat history", gin.H{"messages": []models.ChatMessage{}})
			return
		}
		utils.ServerError(c, err)
		return
	}
	// Do not return ChatMessage.SessionID. The HttpOnly cookie is the only
	// guest-session identifier and remains inaccessible to JavaScript.
	guestMessages := make([]gin.H, 0, len(messages))
	for _, message := range messages {
		guestMessages = append(guestMessages, gin.H{"role": message.Role, "content": message.Content})
	}
	auth.SetGuestSessionCookie(c, h.Services.Config, id.String(), int(h.Services.Config.GuestSessionTTL.Seconds()))
	utils.Success(c, http.StatusOK, "Chat history", gin.H{"messages": guestMessages})
}

func resolveGuestSession(h *Handler, c *gin.Context) (uuid.UUID, error) {
	var sessionID uuid.UUID
	if cookieID := auth.GetGuestSessionCookie(c); cookieID != "" {
		if parsedID, err := uuid.Parse(cookieID); err == nil {
			sessionID = parsedID
		} else {
			auth.ClearGuestSessionCookie(c, h.Services.Config)
		}
	}
	resolvedID, isNew, err := h.Services.AI.ResolveGuestSession(c.Request.Context(), sessionID)
	if err != nil {
		return uuid.Nil, err
	}
	if isNew {
		auth.SetGuestSessionCookie(c, h.Services.Config, resolvedID.String(), int(h.Services.Config.GuestSessionTTL.Seconds()))
	}
	return resolvedID, nil
}
