package services

import (
	"testing"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/mcp"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// TestFormatAILogTrackingCode locks the user-facing tracking code format. The
// first 8 hex chars of the AILog UUID are surfaced so support can correlate
// logs without exposing the full internal ID. A Nil UUID (persist failed)
// falls back to a code-shaped token so the user still receives a code.
func TestFormatAILogTrackingCode(t *testing.T) {
	id := uuid.MustParse("12345678-1234-1234-1234-1234567890ab")
	got := formatAILogTrackingCode(id)
	if got != "AILog-12345678" {
		t.Fatalf("expected AILog-12345678, got %q", got)
	}

	// Nil UUID (e.g. persist failed) must still return a code-shaped token,
	// never an empty string, so the user is never left without a code.
	nilCode := formatAILogTrackingCode(uuid.Nil)
	if nilCode != "AILog-unknown" {
		t.Fatalf("expected AILog-unknown for nil id, got %q", nilCode)
	}
}

// TestFailedSearchTripsAlreadySelected locks the tool-failure surfacing guard
// (task spec): when search_trips returns status=failed with the "a package is
// already selected" business reason, finalizeChat must be able to extract the
// selected package title to tell the user WHICH package is selected + offer
// options. Other failed reasons (session not found, list trips error) must NOT
// trigger the surfacing guard.
func TestFailedSearchTripsAlreadySelected(t *testing.T) {
	title, found := failedSearchTripsAlreadySelected([]ToolResult{
		{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{
			"error":               "a package is already selected",
			"selected_trip_id":    "abc",
			"selected_trip_title": "Bali Adventure 3D2N",
		}},
	})
	if !found {
		t.Fatal("expected to find already-selected failure")
	}
	if title != "Bali Adventure 3D2N" {
		t.Fatalf("expected title 'Bali Adventure 3D2N', got %q", title)
	}

	// A different failure reason must not match — only the explicit
	// "already selected" business reason triggers option surfacing.
	_, found2 := failedSearchTripsAlreadySelected([]ToolResult{
		{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{
			"error": "session not found",
		}},
	})
	if found2 {
		t.Fatal("must not trigger surfacing for non-already-selected failures")
	}

	// Success results are irrelevant to the surfacing guard.
	_, found3 := failedSearchTripsAlreadySelected([]ToolResult{
		{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
			"packages": []map[string]interface{}{},
		}},
	})
	if found3 {
		t.Fatal("must not trigger surfacing for successful search_trips")
	}

	// Missing title (e.g. trip lookup failed in the tool) still returns found=true
	// with an empty title; finalizeChat falls back to a generic phrasing.
	emptyTitle, found4 := failedSearchTripsAlreadySelected([]ToolResult{
		{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{
			"error": "a package is already selected",
		}},
	})
	if !found4 {
		t.Fatal("expected found=true even when title missing")
	}
	if emptyTitle != "" {
		t.Fatalf("expected empty title, got %q", emptyTitle)
	}
}

// TestResponseMentionsSelectionOptions locks the backstop rule: finalizeChat
// only overwrites the model response when the model IGNORED the failed tool
// result entirely. A reasonable LLM answer that already mentions the conflict
// (already-selected / alternatives / cancel / continue) is preserved.
func TestResponseMentionsSelectionOptions(t *testing.T) {
	cases := []struct {
		name string
		resp string
		want bool
	}{
		{"mentions already selected", "Anda sudah memilih paket ini.", true},
		{"mentions alternatives", "Mau lihat alternatif lain?", true},
		{"mentions cancel", "Bisa batalkan pilihan lalu cari lagi.", true},
		{"mentions continue", "Lanjutkan pemesanan paket ini?", true},
		{"ignores conflict entirely", "Ini rekomendasi paket terbaik untuk Anda.", false},
		{"empty response", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := responseMentionsSelectionOptions(tc.resp); got != tc.want {
				t.Fatalf("responseMentionsSelectionOptions(%q) = %v, want %v", tc.resp, got, tc.want)
			}
		})
	}
}

// TestHasSuccessfulInfoTool locks AIW-7: when an informational read tool
// (get_trip_detail / calculate_trip_price / check_trip_availability) succeeds
// in the same round as a stray failed search_trips, finalizeChat must NOT
// overwrite the informative answer with the "already selected" conflict
// backstop. The user asked a detail/price/availability question about the
// selected package — not a new search.
func TestHasSuccessfulInfoTool(t *testing.T) {
	failedSearch := ToolResult{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{
		"error": "a package is already selected",
	}}

	// get_trip_detail success -> info present.
	if !hasSuccessfulInfoTool([]ToolResult{failedSearch, {Tool: mcp.ToolGetTripDetail, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{"title": "Bali"}}}) {
		t.Fatal("expected true when get_trip_detail succeeded")
	}
	// calculate_trip_price success -> info present.
	if !hasSuccessfulInfoTool([]ToolResult{{Tool: mcp.ToolCalculateTripPrice, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{"total": 1.0}}}) {
		t.Fatal("expected true when calculate_trip_price succeeded")
	}
	// check_trip_availability success -> info present.
	if !hasSuccessfulInfoTool([]ToolResult{{Tool: mcp.ToolCheckTripAvailability, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{"available": true}}}) {
		t.Fatal("expected true when check_trip_availability succeeded")
	}
	// Only failed search -> no info tool.
	if hasSuccessfulInfoTool([]ToolResult{failedSearch}) {
		t.Fatal("expected false when only a failed search_trips is present")
	}
	// Info tool that FAILED does not count.
	if hasSuccessfulInfoTool([]ToolResult{{Tool: mcp.ToolGetTripDetail, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "trip not found"}}}) {
		t.Fatal("expected false when the info tool failed")
	}
	// create_booking success is not an info tool.
	if hasSuccessfulInfoTool([]ToolResult{{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{"success": true}}}) {
		t.Fatal("expected false for create_booking (not an info tool)")
	}
}
