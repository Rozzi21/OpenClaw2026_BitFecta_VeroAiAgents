package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/mcp"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

// --- Mocks (SEC-27: MCPService depends on narrow interfaces, so tests mock
// them without a DB). ---

type mockMCPRepo struct {
	trip     models.Trip
	tripErr  error
	messages []models.ChatMessage // captured chat messages (for order marker)
}

func (m *mockMCPRepo) FindTrip(_ context.Context, id uuid.UUID) (models.Trip, error) {
	if m.tripErr != nil {
		return models.Trip{}, m.tripErr
	}
	return m.trip, nil
}
func (m *mockMCPRepo) ListTrips(_ context.Context, _ repositories.TripRepositoryFilter) ([]models.Trip, error) {
	return []models.Trip{m.trip}, nil
}

// ChatRepository / LogRepository stubs (unused by the read tools but required
// to satisfy the MCPRepository interface).
func (m *mockMCPRepo) FindChatSession(_ context.Context, id uuid.UUID) (models.ChatSession, error) {
	return models.ChatSession{BaseModel: models.BaseModel{ID: id}}, nil
}
func (m *mockMCPRepo) CreateChatSession(_ context.Context, s *models.ChatSession) error { return nil }
func (m *mockMCPRepo) UpdateChatSession(_ context.Context, _ *models.ChatSession) error { return nil }
func (m *mockMCPRepo) AddChatMessage(_ context.Context, msg *models.ChatMessage) error {
	m.messages = append(m.messages, *msg)
	return nil
}
func (m *mockMCPRepo) ListChatMessages(_ context.Context, _ uuid.UUID) ([]models.ChatMessage, error) {
	return m.messages, nil
}
func (m *mockMCPRepo) ListRecentChatMessages(_ context.Context, _ uuid.UUID, _ int) ([]models.ChatMessage, error) {
	return m.messages, nil
}
func (m *mockMCPRepo) TailChatMessages(_ context.Context, _ uuid.UUID, _ int) ([]models.ChatMessage, error) {
	return nil, nil
}
func (m *mockMCPRepo) CountChatMessages(_ context.Context, _ uuid.UUID) (int64, error) { return 0, nil }
func (m *mockMCPRepo) ListChatSessions(_ context.Context, _ uuid.UUID) ([]models.ChatSession, error) {
	return nil, nil
}
func (m *mockMCPRepo) UpdateChatSessionMemorySummary(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (m *mockMCPRepo) UpdateChatSessionSelectedTrip(_ context.Context, _ uuid.UUID, _ *uuid.UUID) error {
	return nil
}
func (m *mockMCPRepo) UpdateChatSessionActivity(_ context.Context, _ uuid.UUID, _ time.Time, _ time.Time) error {
	return nil
}
func (m *mockMCPRepo) DeleteExpiredChatSessions(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (m *mockMCPRepo) CountExpiredChatSessions(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (m *mockMCPRepo) FindBookingBySession(_ context.Context, _ uuid.UUID) (models.Booking, error) {
	return models.Booking{}, errors.New("none")
}
func (m *mockMCPRepo) CreateToolCall(_ context.Context, _ *models.ToolCall) error { return nil }
func (m *mockMCPRepo) CreateAILog(_ context.Context, _ *models.AILog) error       { return nil }
func (m *mockMCPRepo) ListAILogs(_ context.Context, _ repositories.RepositoryFilter) ([]models.AILog, error) {
	return nil, nil
}
func (m *mockMCPRepo) ListToolCalls(_ context.Context, _ repositories.RepositoryFilter) ([]models.ToolCall, error) {
	return nil, nil
}

type mockBookingCreator struct {
	createCalls int
}

func (b *mockBookingCreator) Create(_ context.Context, _ uuid.UUID, req dto.BookingRequest) (models.Booking, error) {
	b.createCalls++
	return models.Booking{
		BaseModel:     models.BaseModel{ID: uuid.New()},
		TripID:        req.TripID,
		BookingStatus: models.BookingStatusPending,
		PaymentStatus: models.PaymentStatusPendingAdminProcessing,
		AdultPax:      req.AdultPax,
		ChildPax:      req.ChildPax,
		TotalPrice:    720000,
	}, nil
}

type mockGuestUser struct{}

func (g *mockGuestUser) GuestUser(_ context.Context) (models.User, error) {
	return models.User{BaseModel: models.BaseModel{ID: uuid.New()}}, nil
}

func newTestMCP(repo MCPRepository) *MCPService {
	return &MCPService{
		repo:     repo,
		bus:      events.NewBus(),
		bookings: &mockBookingCreator{},
		auth:     &mockGuestUser{},
		audit:    nil, // nil -> synchronous persist via mocked CreateToolCall/CreateAILog
	}
}

// Trip fixture WITH adult+child discount active.
func discountedTrip() models.Trip {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	return models.Trip{
		BaseModel:            models.BaseModel{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111")},
		Title:                "Bali Adventure",
		Slug:                 "bali-adventure",
		Destination:          "Bali",
		Location:             "Ubud",
		Category:             "domestic",
		Status:               "published",
		Duration:             "3D2N",
		BasePrice:            2000000,
		DiscountPrice:        1500000,
		DiscountEnabled:      true,
		ChildPrice:           1000000,
		ChildDiscount:        750000,
		ChildDiscountEnabled: true,
		AdultPax:             10,
		ChildPax:             5,
		PackageStartDate:     &start,
		PackageEndDate:       &end,
		Highlights:           []string{"Rafting", "Temple"},
		AmenitiesIncluded:    []string{"Hotel", "Meals"},
		AmenitiesExcluded:    []string{"Flights"},
		Itineraries: []models.Itinerary{
			{Day: 1, Title: "Arrive", Description: "Pickup"},
			{Day: 2, Title: "Tour", Description: "Rafting"},
		},
	}
}

// Trip fixture WITHOUT any discount.
func plainTrip() models.Trip {
	t := discountedTrip()
	t.DiscountEnabled = false
	t.DiscountPrice = 0
	t.ChildDiscountEnabled = false
	t.ChildDiscount = 0
	return t
}

func getf(t *testing.T, d map[string]interface{}, key string) float64 {
	t.Helper()
	v, ok := d[key].(float64)
	if !ok {
		t.Fatalf("expected float64 %q, got %#v", key, d[key])
	}
	return v
}

// TestPriceBreakdownDiscount locks the shared pricing math for a discounted
// trip: effective unit prices honor discounts, subtotals and total follow.
func TestPriceBreakdownDiscount(t *testing.T) {
	pb := priceBreakdown(discountedTrip(), 2, 1)
	if pb.AdultUnitPrice != 1500000 {
		t.Fatalf("adult unit should be discount 1500000, got %v", pb.AdultUnitPrice)
	}
	if pb.ChildUnitPrice != 750000 {
		t.Fatalf("child unit should be discount 750000, got %v", pb.ChildUnitPrice)
	}
	if pb.AdultNormalPrice != 2000000 || pb.ChildNormalPrice != 1000000 {
		t.Fatalf("normal prices wrong: adult=%v child=%v", pb.AdultNormalPrice, pb.ChildNormalPrice)
	}
	if !pb.AdultDiscountEnabled || !pb.ChildDiscountEnabled {
		t.Fatal("both discounts should be enabled")
	}
	want := 1500000.0*2 + 750000.0*1
	if pb.Total != want {
		t.Fatalf("total should be %v, got %v", want, pb.Total)
	}
}

// TestPriceBreakdownNoDiscount locks fallback to normal prices when discounts off.
func TestPriceBreakdownNoDiscount(t *testing.T) {
	pb := priceBreakdown(plainTrip(), 1, 2)
	if pb.AdultUnitPrice != 2000000 || pb.ChildUnitPrice != 1000000 {
		t.Fatalf("unit prices should be normal: adult=%v child=%v", pb.AdultUnitPrice, pb.ChildUnitPrice)
	}
	if pb.AdultDiscountEnabled || pb.ChildDiscountEnabled {
		t.Fatal("no discount should be enabled")
	}
	want := 2000000.0*1 + 1000000.0*2
	if pb.Total != want {
		t.Fatalf("total should be %v, got %v", want, pb.Total)
	}
}

// TestCalculateTripPriceMatchesBooking locks the source-of-truth guarantee:
// the tool's total must equal the booking Create total for the same pax.
func TestCalculateTripPriceMatchesBooking(t *testing.T) {
	trip := discountedTrip()
	repo := &mockMCPRepo{trip: trip}
	svc := newTestMCP(repo)

	res, err := svc.Execute(context.Background(), uuid.New(), mcp.ToolCalculateTripPrice, map[string]interface{}{
		"trip_id":   trip.ID.String(),
		"adult_pax": 2,
		"child_pax": 1,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Status != models.ToolResultStatusSuccess {
		t.Fatalf("expected success, got %v (%v)", res.Status, res.Data["error"])
	}
	toolTotal := getf(t, res.Data, "total")

	// Booking source of truth: same helper path.
	bookingTotal := priceBreakdown(trip, 2, 1).Total
	if toolTotal != bookingTotal {
		t.Fatalf("tool total %v != booking total %v (must be identical)", toolTotal, bookingTotal)
	}
	// Sanity: discount applied (not the naive normal-price sum).
	if toolTotal == 2000000*2+1000000*1 {
		t.Fatal("tool total ignored discount")
	}
}

// TestCalculateTripPriceAdultChildCombination verifies adult+child math and the
// per-line breakdown fields the AI reads.
func TestCalculateTripPriceAdultChildCombination(t *testing.T) {
	trip := plainTrip()
	svc := newTestMCP(&mockMCPRepo{trip: trip})
	res, _ := svc.Execute(context.Background(), uuid.New(), mcp.ToolCalculateTripPrice, map[string]interface{}{
		"trip_id": trip.ID.String(), "adult_pax": 3, "child_pax": 2,
	})
	if getf(t, res.Data, "adult_subtotal") != 2000000*3 {
		t.Fatalf("adult_subtotal wrong: %v", res.Data["adult_subtotal"])
	}
	if getf(t, res.Data, "child_subtotal") != 1000000*2 {
		t.Fatalf("child_subtotal wrong: %v", res.Data["child_subtotal"])
	}
	if getf(t, res.Data, "total") != 2000000*3+1000000*2 {
		t.Fatalf("total wrong: %v", res.Data["total"])
	}
}

// TestGetTripDetail verifies the detail tool exposes itinerary, amenities,
// pricing and discount info, and sanitizes free-text.
func TestGetTripDetail(t *testing.T) {
	trip := discountedTrip()
	svc := newTestMCP(&mockMCPRepo{trip: trip})
	res, _ := svc.Execute(context.Background(), uuid.New(), mcp.ToolGetTripDetail, map[string]interface{}{
		"trip_id": trip.ID.String(),
	})
	if res.Status != models.ToolResultStatusSuccess {
		t.Fatalf("expected success, got %v", res.Data["error"])
	}
	if res.Data["discount_enabled"] != true {
		t.Fatal("discount_enabled should be true")
	}
	if getf(t, res.Data, "discount_price") != 1500000 {
		t.Fatalf("discount_price wrong: %v", res.Data["discount_price"])
	}
	if getf(t, res.Data, "child_price") != 1000000 {
		t.Fatalf("child_price wrong: %v", res.Data["child_price"])
	}
	if it, ok := res.Data["itinerary"].([]map[string]interface{}); !ok || len(it) != 2 {
		t.Fatalf("expected 2 itinerary entries, got %#v", res.Data["itinerary"])
	}
	if am, ok := res.Data["amenities_included"].([]string); !ok || len(am) != 2 {
		t.Fatalf("expected 2 included amenities, got %#v", res.Data["amenities_included"])
	}
	if res.Data["adult_pax_quota"] != 10 {
		t.Fatalf("expected adult quota 10, got %#v", res.Data["adult_pax_quota"])
	}
}

// TestCheckTripAvailabilityWindow covers: in-window (available), before window,
// after window, over-quota, and not-published.
func TestCheckTripAvailabilityWindow(t *testing.T) {
	trip := discountedTrip() // published, window 2026-09-01..2026-12-31, quota 10/5
	svc := newTestMCP(&mockMCPRepo{trip: trip})
	call := func(date string, adult, child int) map[string]interface{} {
		res, _ := svc.Execute(context.Background(), uuid.New(), mcp.ToolCheckTripAvailability, map[string]interface{}{
			"trip_id": trip.ID.String(), "travel_date": date, "adult_pax": adult, "child_pax": child,
		})
		if res.Status != models.ToolResultStatusSuccess {
			t.Fatalf("availability call failed: %v", res.Data["error"])
		}
		return res.Data
	}

	if d := call("2026-10-10", 2, 1); d["available"] != true {
		t.Fatalf("in-window should be available, got %#v", d["reasons"])
	}
	if d := call("2026-08-01", 2, 1); d["available"] != false {
		t.Fatal("before window should be unavailable")
	}
	if d := call("2027-01-15", 2, 1); d["available"] != false {
		t.Fatal("after window should be unavailable")
	}
	if d := call("2026-10-10", 11, 1); d["available"] != false {
		t.Fatal("over adult quota should be unavailable")
	}
	if d := call("2026-10-10", 2, 6); d["available"] != false {
		t.Fatal("over child quota should be unavailable")
	}

	// Not published trip is never available.
	draft := plainTrip()
	draft.Status = "draft"
	svcDraft := newTestMCP(&mockMCPRepo{trip: draft})
	res, _ := svcDraft.Execute(context.Background(), uuid.New(), mcp.ToolCheckTripAvailability, map[string]interface{}{
		"trip_id": draft.ID.String(), "travel_date": "2026-10-10", "adult_pax": 1, "child_pax": 0,
	})
	if res.Data["available"] != false {
		t.Fatal("draft package should be unavailable")
	}
}

// TestToolsTripNotFound locks the AI-safe error for a missing/invalid trip_id
// across all three read tools (no guessed data, no raw DB error leaked).
func TestToolsTripNotFound(t *testing.T) {
	svc := newTestMCP(&mockMCPRepo{tripErr: errors.New("record not found")})
	tools := []string{mcp.ToolGetTripDetail, mcp.ToolCalculateTripPrice, mcp.ToolCheckTripAvailability}
	for _, tool := range tools {
		res, _ := svc.Execute(context.Background(), uuid.New(), tool, map[string]interface{}{
			"trip_id": uuid.New().String(), "travel_date": "2026-10-10", "adult_pax": 1, "child_pax": 0,
		})
		if res.Status != models.ToolResultStatusFailed {
			t.Fatalf("%s: expected failed for missing trip, got %v", tool, res.Status)
		}
		if res.Data["error"] != "trip not found" {
			t.Fatalf("%s: expected 'trip not found', got %#v", tool, res.Data["error"])
		}
	}
	// Invalid (non-UUID) trip_id.
	res, _ := svc.Execute(context.Background(), uuid.New(), mcp.ToolGetTripDetail, map[string]interface{}{"trip_id": "not-a-uuid"})
	if res.Data["error"] != "invalid trip_id" {
		t.Fatalf("expected 'invalid trip_id', got %#v", res.Data["error"])
	}
}

// TestCheckTripAvailabilityInvalidDate verifies a bad date yields an error the
// AI can act on, not an availability guess.
func TestCheckTripAvailabilityInvalidDate(t *testing.T) {
	svc := newTestMCP(&mockMCPRepo{trip: discountedTrip()})
	res, _ := svc.Execute(context.Background(), uuid.New(), mcp.ToolCheckTripAvailability, map[string]interface{}{
		"trip_id": discountedTrip().ID.String(), "travel_date": "bukan-tanggal", "adult_pax": 1,
	})
	if res.Status != models.ToolResultStatusFailed {
		t.Fatal("expected failed for invalid date")
	}
}

// TestSearchTripsExposesPricing locks that the catalog payload now carries the
// safe pricing fields (discount + child price) so the AI can answer those
// questions grounded in backend data.
func TestSearchTripsExposesPricing(t *testing.T) {
	trip := discountedTrip()
	svc := newTestMCP(&mockMCPRepo{trip: trip})
	res, _ := svc.Execute(context.Background(), uuid.New(), mcp.ToolSearchTrips, map[string]interface{}{"query": "bali"})
	if res.Status != models.ToolResultStatusSuccess {
		t.Fatalf("search failed: %v", res.Data["error"])
	}
	pkgs, ok := res.Data["packages"].([]map[string]interface{})
	if !ok || len(pkgs) == 0 {
		t.Fatalf("expected packages, got %#v", res.Data["packages"])
	}
	p := pkgs[0]
	if p["discount_enabled"] != true {
		t.Fatal("search_trips must expose discount_enabled")
	}
	if getf(t, p, "discount_price") != 1500000 {
		t.Fatalf("discount_price wrong: %v", p["discount_price"])
	}
	if getf(t, p, "child_price") != 1000000 {
		t.Fatalf("child_price wrong: %v", p["child_price"])
	}
	if getf(t, p, "adult_effective_price") != 1500000 {
		t.Fatalf("adult_effective_price should honor discount: %v", p["adult_effective_price"])
	}
}

// --- AIW-8: order status + duplicate-order guard. ---

// TestCheckOrderStatusEmpty: no marker -> order_exists=false.
func TestCheckOrderStatusEmpty(t *testing.T) {
	svc := newTestMCP(&mockMCPRepo{trip: discountedTrip()})
	res, _ := svc.Execute(context.Background(), uuid.New(), mcp.ToolCheckOrderStatus, map[string]interface{}{})
	if res.Status != models.ToolResultStatusSuccess {
		t.Fatalf("expected success, got %v", res.Data["error"])
	}
	if res.Data["order_exists"] != false {
		t.Fatalf("expected order_exists=false, got %#v", res.Data["order_exists"])
	}
}

// TestCreateBookingThenCheckOrderStatus: a successful create_booking writes an
// order marker; check_order_status then reports the SAME order id; a second
// create_booking is blocked (no duplicate).
func TestCreateBookingThenCheckOrderStatus(t *testing.T) {
	repo := &mockMCPRepo{trip: discountedTrip()}
	creator := &mockBookingCreator{}
	svc := &MCPService{repo: repo, bus: events.NewBus(), bookings: creator, auth: &mockGuestUser{}, audit: nil}
	sessionID := uuid.New()
	trip := discountedTrip()

	bookingPayload := map[string]interface{}{
		"trip_id":       trip.ID.String(),
		"adult_pax":     2,
		"child_pax":     0,
		"travel_date":   "2026-10-10",
		"contact_name":  "Ozi",
		"contact_phone": "081755245245",
	}

	// First create_booking succeeds.
	res1, _ := svc.Execute(context.Background(), sessionID, mcp.ToolCreateBooking, bookingPayload)
	if res1.Status != models.ToolResultStatusSuccess {
		t.Fatalf("first create_booking failed: %v", res1.Data["error"])
	}
	orderID, _ := res1.Data["order_id"].(string)
	if orderID == "" {
		t.Fatal("expected order_id in create_booking result")
	}
	if creator.createCalls != 1 {
		t.Fatalf("expected 1 booking create call, got %d", creator.createCalls)
	}

	// check_order_status reports the order with the same id.
	st, _ := svc.Execute(context.Background(), sessionID, mcp.ToolCheckOrderStatus, map[string]interface{}{})
	if st.Data["order_exists"] != true {
		t.Fatalf("expected order_exists=true after booking, got %#v", st.Data)
	}
	if got, _ := st.Data["order_id"].(string); got != orderID {
		t.Fatalf("check_order_status order_id %q != created %q", got, orderID)
	}

	// Second create_booking is blocked and does NOT create another booking.
	res2, _ := svc.Execute(context.Background(), sessionID, mcp.ToolCreateBooking, bookingPayload)
	if res2.Status != models.ToolResultStatusFailed {
		t.Fatalf("expected duplicate create_booking to be blocked, got %v", res2.Status)
	}
	if res2.Data["order_exists"] != true {
		t.Fatalf("expected order_exists=true on duplicate, got %#v", res2.Data)
	}
	if creator.createCalls != 1 {
		t.Fatalf("duplicate must not call Create again; createCalls=%d", creator.createCalls)
	}
	if got, _ := res2.Data["order_id"].(string); got != orderID {
		t.Fatalf("duplicate should return existing order_id %q, got %q", orderID, got)
	}
}
