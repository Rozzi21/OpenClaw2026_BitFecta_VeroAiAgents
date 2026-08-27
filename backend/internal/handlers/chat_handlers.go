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
	chatCtx := services.ChatContext{SessionID: *req.SessionID, UserID: &userID}

	// PERF-1: streaming path. The authenticated endpoint does not manage
	// cookies, so setCookie is nil.
	if req.Stream {
		h.streamChat(c, chatCtx, req, nil)
		return
	}

	res, err := h.Services.AI.Chat(c.Request.Context(), chatCtx, req)
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
	identity, err := h.Services.Guests.Resolve(c.Request.Context(), auth.GetGuestIdentityCookie(c))
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	if err := h.Services.Guests.AttachChat(c.Request.Context(), sessionID, identity.Session.ID); err != nil {
		utils.ServerError(c, err)
		return
	}
	auth.SetGuestIdentityCookie(c, h.Services.Config, identity.Token, int(h.Services.Config.GuestIdentityTTL.Seconds()))

	// OptionalAuth may have authenticated this request. The guest cookie is
	// still the session ownership proof (GuestCookieBound); the user ID only
	// upgrades order attribution so a signed-in customer (password or Google)
	// can create orders beyond the one-order guest limit from the chat.
	chatCtx := services.ChatContext{SessionID: sessionID, GuestCookieBound: true}
	if uid := currentUserID(c); uid != uuid.Nil {
		chatCtx.UserID = &uid
	}

	// PERF-1: streaming path. The guest session cookie must be set BEFORE the
	// first byte of the SSE body is written (headers cannot change after the
	// body starts), so we pass a setCookie callback that streamChat invokes
	// after setting SSE headers. We always refresh (sliding TTL) so a long
	// streaming session keeps the cookie alive; if ChatStream later reports
	// the session expired/not-found the client receives an `error` event and
	// the next request resolves a fresh session (resolveGuestSession ignores
	// an expired cookie value).
	if req.Stream {
		h.streamChat(c, chatCtx, req, func() {
			auth.SetGuestSessionCookie(c, h.Services.Config, sessionID.String(), int(h.Services.Config.GuestSessionTTL.Seconds()))
		})
		return
	}

	res, err := h.Services.AI.Chat(c.Request.Context(), chatCtx, req)
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
