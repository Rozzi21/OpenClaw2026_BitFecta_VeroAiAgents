package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/ai"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/mcp"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// Guest-order rule as seen by the AI/MCP layer.
//
// The rule itself (a guest may create exactly ONE order) is enforced by
// BookingService inside the booking transaction and pinned by
// guest_order_limit_test.go / guest_order_contact_entitlement_test.go. What this
// file locks is the LAYER ABOVE it — that the refusal travels outward without
// being reinterpreted:
//
//  1. MCP translates ErrGuestOrderLimitReached into a STRUCTURED tool result
//     (code + status), never a bare sentence, and leaks no internal error text.
//  2. The AI tool loop refuses a create_booking RETRY after that code
//     deterministically: the second call never reaches MCP, so it never reaches
//     BookingService or the database — even when the model varies the arguments.
//  3. The chat transport reports one structured gate (ChatOrderGate) the client
//     can branch on, and that gate never carries an order id it is not allowed
//     to reveal.
//
// No LLM and no network: the AI client is never constructed, so no
// OPENAI_API_KEY is needed. MCP and the booking domain are mocked through the
// narrow SEC-27 interfaces.

// limitedBookingCreator is the booking domain with a spent guest allowance: the
// guest path refuses, the authenticated path succeeds (proving the refusal is
// about the guest identity, not the request).
type limitedBookingCreator struct {
	guestCalls int
	userCalls  int
}

func (b *limitedBookingCreator) Create(_ context.Context, userID uuid.UUID, _ string, req dto.BookingRequest) (models.Booking, error) {
	b.userCalls++
	return models.Booking{
		BaseModel:     models.BaseModel{ID: uuid.New()},
		UserID:        userID,
		TripID:        req.TripID,
		BookingStatus: models.BookingStatusPending,
		PaymentStatus: models.PaymentStatusPendingAdminProcessing,
		TotalPrice:    1000,
	}, nil
}

func (b *limitedBookingCreator) CreateGuest(_ context.Context, _, _ uuid.UUID, _ string, _ dto.BookingRequest) (models.Booking, error) {
	b.guestCalls++
	return models.Booking{}, ErrGuestOrderLimitReached
}

func createBookingPayload(contact string) map[string]interface{} {
	return map[string]interface{}{
		"trip_id":       "11111111-1111-1111-1111-111111111111",
		"adult_pax":     1,
		"child_pax":     0,
		"travel_date":   "2026-12-01",
		"contact_name":  "Guest",
		"contact_email": contact,
	}
}

// TestCreateBookingGuestLimit_StructuredToolResult: the guest limit reaches the
// LLM as data, not prose. `code` is the stable token both the model and the
// frontend branch on; `status` says what unblocks it. The raw sentinel error
// text must not appear anywhere in the payload — the model would then be tempted
// to relay backend internals to the user.
func TestCreateBookingGuestLimit_StructuredToolResult(t *testing.T) {
	repo := &mockMCPRepo{trip: discountedTrip()}
	creator := &limitedBookingCreator{}
	svc := &MCPService{repo: repo, bus: events.NewBus(), bookings: creator, auth: &mockGuestUser{}, audit: nil}

	result := svc.executeCreateBooking(context.Background(), uuid.New(), nil, createBookingPayload("guest@example.com"))

	if creator.guestCalls != 1 {
		t.Fatalf("guest booking path should be attempted exactly once, got %d", creator.guestCalls)
	}
	if result.Status != models.ToolResultStatusFailed {
		t.Fatalf("expected failed tool result, got %q", result.Status)
	}
	if result.Data["code"] != CodeGuestOrderLimitReached {
		t.Fatalf("expected code %q, got %v", CodeGuestOrderLimitReached, result.Data["code"])
	}
	if result.Data["status"] != "requires_authentication" {
		t.Fatalf("expected status requires_authentication, got %v", result.Data["status"])
	}
	if success, ok := result.Data["success"].(bool); !ok || success {
		t.Fatalf("expected success=false, got %v", result.Data["success"])
	}
	if result.Data["order_id"] != nil {
		t.Fatalf("limit result must not carry an order id (it may belong to another guest identity): %v", result.Data["order_id"])
	}
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	if strings.Contains(string(serialized), ErrGuestOrderLimitReached.Error()) {
		t.Fatalf("internal error text leaked into the tool result: %s", serialized)
	}
}

// TestCreateBookingAuthenticated_NoGuestLimit: the same MCP call by a signed-in
// customer goes down BookingService.Create — no guest allowance, no gate. This
// is the other half of the contract the frontend depends on ("sign in, then
// order again"), and it proves the guard added below cannot fire for accounts.
func TestCreateBookingAuthenticated_NoGuestLimit(t *testing.T) {
	repo := &mockMCPRepo{trip: discountedTrip()}
	creator := &limitedBookingCreator{}
	svc := &MCPService{repo: repo, bus: events.NewBus(), bookings: creator, auth: &mockGuestUser{}, audit: nil}
	userID := uuid.New()

	result := svc.executeCreateBooking(context.Background(), uuid.New(), &userID, createBookingPayload("member@example.com"))

	if creator.userCalls != 1 || creator.guestCalls != 0 {
		t.Fatalf("authenticated call must use the account path: user=%d guest=%d", creator.userCalls, creator.guestCalls)
	}
	if result.Status != models.ToolResultStatusSuccess || result.Data["code"] != CodeOrderCreated {
		t.Fatalf("expected a created order, got status=%q code=%v", result.Status, result.Data["code"])
	}
	if orderID, _ := result.Data["order_id"].(string); orderID == "" {
		t.Fatal("successful create_booking must expose order_id so the client can track it")
	}
}

// countingExecutor records every tool dispatch that reaches MCP. Any increment
// after a guest-limit refusal means a retry got through to the booking domain.
type countingExecutor struct {
	calls int
}

func (e *countingExecutor) Execute(_ context.Context, _ uuid.UUID, _ *uuid.UUID, toolName string, _ map[string]interface{}) (ToolResult, error) {
	e.calls++
	return ToolResult{Tool: toolName, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{"ok": true}}, nil
}

func limitToolResult() ToolResult {
	return ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{
		"success": false, "status": "requires_authentication", "code": CodeGuestOrderLimitReached,
	}}
}

func toolCall(name, args string) ai.ToolCall {
	tc := ai.ToolCall{ID: "call-" + name}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

// TestExecuteToolCall_NoRetryAfterGuestOrderLimit: the AI must not retry
// create_booking after GUEST_ORDER_LIMIT_REACHED, and "must not" is enforced by
// code rather than by the prompt. The retry carries DIFFERENT arguments (a fresh
// contact — exactly what a model would try), so the AIW-3 dedup map cannot catch
// it; the guard refuses it before MCP is touched and hands the model back the
// same structured code.
func TestExecuteToolCall_NoRetryAfterGuestOrderLimit(t *testing.T) {
	exec := &countingExecutor{}
	svc := &AIService{mcp: exec, bus: events.NewBus()}
	prior := []ToolResult{limitToolResult()}
	called := map[string]bool{}

	retry := toolCall(mcp.ToolCreateBooking, `{"trip_id":"11111111-1111-1111-1111-111111111111","contact_email":"another@example.com"}`)
	result, msg := svc.executeToolCall(context.Background(), uuid.New(), nil, retry, called, prior)

	if exec.calls != 0 {
		t.Fatalf("retry reached MCP/BookingService: %d dispatch(es)", exec.calls)
	}
	if result.Status != models.ToolResultStatusFailed || result.Data["code"] != CodeGuestOrderLimitReached {
		t.Fatalf("blocked retry must repeat the structured code, got status=%q code=%v", result.Status, result.Data["code"])
	}
	if blocked, ok := result.Data["retry_blocked"].(bool); !ok || !blocked {
		t.Fatalf("blocked retry should be marked retry_blocked, got %v", result.Data["retry_blocked"])
	}
	// The tool message fed back to the LLM must contain the code, otherwise the
	// model cannot tell why the call failed and may loop.
	if !strings.Contains(msg.Content, CodeGuestOrderLimitReached) {
		t.Fatalf("tool message for the LLM lost the code: %s", msg.Content)
	}
	if msg.Role != "tool" || msg.ToolCallID != retry.ID {
		t.Fatalf("blocked retry must still answer the tool call: role=%q id=%q", msg.Role, msg.ToolCallID)
	}

	// Non-order tools stay usable after the limit: the guest can keep browsing,
	// asking for details, and reading prices while signed out.
	search := toolCall(mcp.ToolSearchTrips, `{"query":"bali"}`)
	if _, _ = svc.executeToolCall(context.Background(), uuid.New(), nil, search, called, prior); exec.calls != 1 {
		t.Fatalf("unrelated tools must still execute after the limit, dispatches=%d", exec.calls)
	}
}

// TestBlockedRetryAfterGuestOrderLimit_Scope pins exactly when the guard fires.
// Over-blocking would break the first (allowed) guest order; under-blocking
// would let a model burn retries against the database.
func TestBlockedRetryAfterGuestOrderLimit_Scope(t *testing.T) {
	cases := []struct {
		name     string
		prior    []ToolResult
		tool     string
		expected bool
	}{
		{"first guest order: nothing blocked", nil, mcp.ToolCreateBooking, false},
		{"retry after limit is blocked", []ToolResult{limitToolResult()}, mcp.ToolCreateBooking, true},
		{"create_order alias is blocked too", []ToolResult{limitToolResult()}, mcp.ToolCreateOrder, true},
		{"other tools are never blocked", []ToolResult{limitToolResult()}, mcp.ToolSearchTrips, false},
		{"unrelated create_booking failure does not block", []ToolResult{{
			Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed,
			Data: map[string]interface{}{"success": false, "error": "invalid trip_id"},
		}}, mcp.ToolCreateBooking, false},
		{"a limit reported by another tool does not block", []ToolResult{{
			Tool: mcp.ToolCheckOrderStatus, Status: models.ToolResultStatusSuccess,
			Data: map[string]interface{}{"code": CodeGuestOrderLimitReached},
		}}, mcp.ToolCreateBooking, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, blocked := blockedRetryAfterGuestOrderLimit(tc.prior, tc.tool)
			if blocked != tc.expected {
				t.Fatalf("blocked = %v, want %v", blocked, tc.expected)
			}
		})
	}
}

// TestChatOrderGateFromToolResults locks the structured signal the chat client
// renders. The client must never parse the assistant's prose, so this mapping is
// the whole contract: which code, whether sign-in is required, and which order
// id may be revealed.
func TestChatOrderGateFromToolResults(t *testing.T) {
	createdID := uuid.NewString()
	existingID := uuid.NewString()
	created := ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
		"success": true, "code": CodeOrderCreated, "order_id": createdID,
	}}
	duplicate := ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{
		"success": false, "code": CodeOrderAlreadyExists, "order_exists": true, "order_id": existingID,
	}}

	if gate := chatOrderGateFromToolResults(nil); gate != nil {
		t.Fatalf("a turn without create_booking must have no gate, got %+v", gate)
	}
	if gate := chatOrderGateFromToolResults([]ToolResult{{
		Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{"packages": nil},
	}}); gate != nil {
		t.Fatalf("browsing must not raise a gate, got %+v", gate)
	}

	limited := chatOrderGateFromToolResults([]ToolResult{limitToolResult()})
	if limited == nil || limited.Code != CodeGuestOrderLimitReached || !limited.AuthRequired {
		t.Fatalf("guest limit must require auth, got %+v", limited)
	}
	if limited.OrderID != "" {
		t.Fatalf("guest-limit gate must not expose an order id: %q", limited.OrderID)
	}

	createdGate := chatOrderGateFromToolResults([]ToolResult{created})
	if createdGate == nil || createdGate.Code != CodeOrderCreated || createdGate.AuthRequired || createdGate.OrderID != createdID {
		t.Fatalf("created order gate wrong: %+v", createdGate)
	}

	duplicateGate := chatOrderGateFromToolResults([]ToolResult{duplicate})
	if duplicateGate == nil || duplicateGate.Code != CodeOrderAlreadyExists || duplicateGate.AuthRequired || duplicateGate.OrderID != existingID {
		t.Fatalf("duplicate order gate wrong: %+v", duplicateGate)
	}

	// Precedence: a persisted order outranks any refusal recorded in the same
	// turn, so the client keeps offering tracking instead of a sign-in prompt.
	mixed := chatOrderGateFromToolResults([]ToolResult{limitToolResult(), created})
	if mixed == nil || mixed.Code != CodeOrderCreated || mixed.AuthRequired {
		t.Fatalf("a created order must win over a refusal, got %+v", mixed)
	}

	// A code this build does not know about is ignored rather than guessed at.
	if gate := chatOrderGateFromToolResults([]ToolResult{{
		Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed,
		Data: map[string]interface{}{"code": "SOME_FUTURE_CODE"},
	}}); gate != nil {
		t.Fatalf("unknown code must not produce a gate, got %+v", gate)
	}
}
