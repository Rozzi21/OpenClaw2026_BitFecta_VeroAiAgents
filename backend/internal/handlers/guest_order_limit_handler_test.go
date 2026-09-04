package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

// HTTP contract of the one-order guest rule on POST /api/v1/orders.
//
// The rule lives in BookingService (service tests cover the rule itself). What
// this file pins is what CLIENTS see, because the frontend and the MCP layer are
// only allowed to branch on that:
//
//   - the first guest order succeeds without any login;
//   - the second is refused with 403 and `error.code =
//     GUEST_ORDER_LIMIT_REACHED` — a stable token, so the UI never has to read
//     the human-readable message;
//   - the refusal writes nothing (no half-created order left behind).
//
// Harness: the claim suite's SQLite env (setupClaimEnv) plus a router that
// mounts only this endpoint. No JWT, no AI, no network.

type orderEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		ID string `json:"id"`
	} `json:"data"`
	Error struct {
		Code   string `json:"code"`
		Status string `json:"status"`
	} `json:"error"`
}

func newGuestOrderRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/orders", h.GuestCreateOrder)
	return r
}

// postGuestOrder sends a realistic guest order: identity in the HttpOnly cookie,
// an Idempotency-Key header, and a contact that can anchor the entitlement.
func postGuestOrder(t *testing.T, r *gin.Engine, guestToken, idempotencyKey, contactEmail string, tripID uuid.UUID) (int, orderEnvelope) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"trip_id":       tripID,
		"adult_pax":     1,
		"child_pax":     0,
		"contact_name":  "Guest",
		"contact_email": contactEmail,
		"travel_date":   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
	})
	if err != nil {
		t.Fatalf("marshal order body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	if guestToken != "" {
		req.AddCookie(&http.Cookie{Name: guestClaimCookieName, Value: guestToken})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var env orderEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, w.Body.String())
	}
	return w.Code, env
}

func countBookings(t *testing.T, e claimEnv, guestID uuid.UUID) int64 {
	t.Helper()
	var total int64
	if err := e.db.Model(&models.Booking{}).Where("guest_session_id = ?", guestID).Count(&total).Error; err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	return total
}

// TestGuestCreateOrder_FirstAllowed_SecondReturnsStructuredCode is the contract
// the whole integration hangs on: guest order #1 needs no account, guest order
// #2 is answered with a machine-readable code that tells the client to offer
// sign-in.
func TestGuestCreateOrder_FirstAllowed_SecondReturnsStructuredCode(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	_, guest := e.seedGuestIdentity(t, "guest-token")
	r := newGuestOrderRouter(e.h)

	status, first := postGuestOrder(t, r, "guest-token", "http-guest-order-0001", "first@example.com", trip.ID)
	if status != http.StatusCreated || !first.Success {
		t.Fatalf("first guest order must be allowed without login: status=%d env=%+v", status, first)
	}
	if first.Data.ID == "" {
		t.Fatal("first guest order should return the created order id for tracking")
	}

	// Second attempt: same guest identity, fresh idempotency key and a different
	// contact, so nothing but the guest rule can refuse it.
	status, second := postGuestOrder(t, r, "guest-token", "http-guest-order-0002", "second@example.com", trip.ID)
	if status != http.StatusForbidden {
		t.Fatalf("second guest order must be refused with 403, got %d (%+v)", status, second)
	}
	if second.Success {
		t.Fatal("refusal must not be reported as success")
	}
	if second.Error.Code != services.CodeGuestOrderLimitReached {
		t.Fatalf("expected error.code %q, got %q", services.CodeGuestOrderLimitReached, second.Error.Code)
	}
	if second.Error.Status != "authentication_required" {
		t.Fatalf("expected error.status authentication_required, got %q", second.Error.Status)
	}
	if second.Message == "" {
		t.Fatal("a human-readable message should still be present for display")
	}
	if total := countBookings(t, e, guest.ID); total != 1 {
		t.Fatalf("refused order must not be persisted: %d bookings for this guest", total)
	}
}

// TestGuestCreateOrder_ContactAnchorRefusalUsesSameCode: a fresh cookie does not
// buy a second order when the contact is reused (GO-P0-1 anchor). The client
// contract must not change with the reason — same 403, same code — so the UI has
// exactly one branch to maintain.
func TestGuestCreateOrder_ContactAnchorRefusalUsesSameCode(t *testing.T) {
	e := setupClaimEnv(t)
	trip := e.seedTrip(t)
	e.seedGuestIdentity(t, "guest-token-a")
	e.seedGuestIdentity(t, "guest-token-b")
	r := newGuestOrderRouter(e.h)

	shared := "shared.anchor@example.com"
	if status, env := postGuestOrder(t, r, "guest-token-a", "http-anchor-order-0001", shared, trip.ID); status != http.StatusCreated {
		t.Fatalf("first order should succeed: status=%d env=%+v", status, env)
	}

	// Different guest identity (as if the visitor cleared the cookie), same
	// contact: the database-side anchor refuses it.
	status, env := postGuestOrder(t, r, "guest-token-b", "http-anchor-order-0002", shared, trip.ID)
	if status != http.StatusForbidden || env.Error.Code != services.CodeGuestOrderLimitReached {
		t.Fatalf("contact-anchor refusal must reuse the same contract: status=%d code=%q", status, env.Error.Code)
	}

	var total int64
	if err := e.db.Model(&models.Booking{}).Count(&total).Error; err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected exactly one persisted order, got %d", total)
	}
}
