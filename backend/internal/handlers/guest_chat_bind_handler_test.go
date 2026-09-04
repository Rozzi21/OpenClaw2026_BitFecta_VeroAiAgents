package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

// Guest chat → guest identity binding at the handler layer (GO-P2-7).
//
// chat_sessions.guest_session_id decides which guest identity owns an order
// created from that chat (MCP create_booking derives the owner from it), so the
// binding is a single-winner conditional write. This test covers what GuestChat
// does when that write is REFUSED: it must never re-point the existing session
// and never serve it — it mints a fresh session for the caller instead (the same
// policy SEC-17 applies to foreign authenticated session ids).

func newChatTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)
	return c, w
}

func TestGuestChatRebindsWhenSessionOwnedByAnotherIdentity(t *testing.T) {
	e := setupClaimEnv(t)
	_, guestA := e.seedGuestIdentity(t, "chat-token-a")
	_, guestB := e.seedGuestIdentity(t, "chat-token-b")
	ctx := context.Background()

	// Identity A owns the chat session.
	chatA, _, err := e.svc.AI.ResolveGuestSession(ctx, uuid.Nil)
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	if err := e.svc.Guests.AttachChat(ctx, chatA, guestA.ID); err != nil {
		t.Fatalf("bind chat to guest A: %v", err)
	}

	// Identity B presents A's chat cookie: the bind is refused, not applied.
	if err := e.svc.Guests.AttachChat(ctx, chatA, guestB.ID); !errors.Is(err, services.ErrChatSessionGuestMismatch) {
		t.Fatalf("foreign bind must be refused, got %v", err)
	}

	c, w := newChatTestContext(t)
	fresh, err := e.h.rebindGuestChatSession(c, guestB.ID)
	if err != nil {
		t.Fatalf("recovery must produce a usable session: %v", err)
	}
	if fresh == chatA || fresh == uuid.Nil {
		t.Fatalf("recovery must mint a NEW session, got %s (original %s)", fresh, chatA)
	}

	var freshRow models.ChatSession
	if err := e.db.First(&freshRow, "id = ?", fresh).Error; err != nil {
		t.Fatalf("reload fresh chat session: %v", err)
	}
	if freshRow.GuestSessionID == nil || *freshRow.GuestSessionID != guestB.ID {
		t.Fatalf("fresh session not bound to the caller: %v", freshRow.GuestSessionID)
	}

	var originalRow models.ChatSession
	if err := e.db.First(&originalRow, "id = ?", chatA).Error; err != nil {
		t.Fatalf("reload original chat session: %v", err)
	}
	if originalRow.GuestSessionID == nil || *originalRow.GuestSessionID != guestA.ID {
		t.Fatalf("original session was re-pointed: %v", originalRow.GuestSessionID)
	}

	// The caller's cookie must now point at the fresh session, otherwise the
	// next request would present the foreign id again.
	cookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "vero_chat_session="+fresh.String()) {
		t.Fatalf("chat cookie not rewritten to the fresh session: %q", cookie)
	}
	if !strings.Contains(cookie, "HttpOnly") {
		t.Fatalf("chat cookie must stay HttpOnly: %q", cookie)
	}
}
