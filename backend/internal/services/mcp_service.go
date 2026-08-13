package services

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/mcp"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

// SEC-27: MCPService depends on narrow interfaces — a repository contract plus
// inter-service contracts (BookingCreator, GuestUserProvider) — instead of the
// concrete *repositories.Repository / *BookingService / *AuthService. Tests can
// now mock every dependency without a DB or real sibling services.
//
// PERF-3 #2: audit (tool call + AI log) persistence is detached from the
// synchronous response path via a bounded worker pool (AuditPool). When audit
// is nil (e.g. unit tests), Execute falls back to synchronous persist so the
// audit trail is still recorded.
type MCPService struct {
	repo     MCPRepository
	bus      *events.Bus
	bookings BookingCreator
	auth     GuestUserProvider
	audit    *AuditPool
}

// MCPRepository is the repository contract MCPService uses (SEC-27): chat
// session reads, trip catalog search, selected-trip update, and tool/AI audit
// logging. Composed from domain interfaces in repositories/interfaces.go.
type MCPRepository interface {
	repositories.ChatRepository
	repositories.LogRepository
	FindTrip(ctx context.Context, id uuid.UUID) (models.Trip, error)
	ListTrips(ctx context.Context, query repositories.TripRepositoryFilter) ([]models.Trip, error)
}

// BookingCreator is the inter-service contract MCPService uses to create a
// booking via the booking domain (SEC-27). *BookingService satisfies it.
type BookingCreator interface {
	Create(ctx context.Context, userID uuid.UUID, req dto.BookingRequest) (models.Booking, error)
}

// GuestUserProvider is the inter-service contract MCPService uses to resolve a
// guest user for order attribution (SEC-27). *AuthService satisfies it.
type GuestUserProvider interface {
	GuestUser(ctx context.Context) (models.User, error)
}

type ToolResult struct {
	Tool   string                 `json:"tool"`
	Status string                 `json:"status"`
	Data   map[string]interface{} `json:"data"`
}

func (s *MCPService) Execute(ctx context.Context, sessionID uuid.UUID, toolName string, payload map[string]interface{}) (ToolResult, error) {
	start := time.Now()
	var result ToolResult
	log.Printf("[mcp] tool selected session=%s tool=%s payload=%+v", sessionID, toolName, payload)

	switch toolName {
	case mcp.ToolCreatePayment:
		// DOKU/payment tools are temporarily disabled.
		result = ToolResult{Tool: toolName, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "payment tools are temporarily disabled"}}

	// Legacy recommendation mock names route to the same catalog search so a
	// stale LLM tool call still returns real data instead of "unknown tool".
	case mcp.ToolSearchTrips, mcp.ToolSearchDestination, mcp.ToolSearchHotels, mcp.ToolCalculateBudget, mcp.ToolGenerateItinerary:
		result = s.executeSearchTrips(ctx, sessionID, payload)

	case mcp.ToolSelectPackage:
		result = s.executeSelectPackage(ctx, sessionID, payload)

	case mcp.ToolCollectOrderDetail, mcp.ToolUpdateOrderDraft:
		result = s.executeCollectOrderDetail(toolName, payload)

	case mcp.ToolCreateBooking, mcp.ToolCreateOrder:
		result = s.executeCreateBooking(ctx, sessionID, payload)

	// AIW-5: detail / pricing / availability tools.
	case mcp.ToolGetTripDetail:
		result = s.executeGetTripDetail(ctx, payload)
	case mcp.ToolCalculateTripPrice:
		result = s.executeCalculateTripPrice(ctx, payload)
	case mcp.ToolCheckTripAvailability:
		result = s.executeCheckTripAvailability(ctx, payload)
	case mcp.ToolCheckOrderStatus:
		result = s.executeCheckOrderStatus(ctx, sessionID)

	default:
		for attempt := 1; attempt <= 3; attempt++ {
			result = s.mock(toolName, payload)
			if result.Status == models.ToolResultStatusSuccess {
				break
			}
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
	}

	log.Printf("[mcp] tool executed session=%s tool=%s status=%s duration_ms=%d", sessionID, toolName, result.Status, time.Since(start).Milliseconds())

	// PERF-3 #2: Persist tool call + AI log audit trail asynchronously via a
	// bounded worker pool, detached from the synchronous LLM response path.
	// json.Marshal + DB writes happen off the request goroutine. The pool is
	// bounded (workers + buffer) so high tool-call volume cannot flood goroutines
	// or starve the DB connection pool (SEC-21 note). When no pool is wired
	// (unit tests), fall back to synchronous persist so the audit trail is still
	// recorded.
	//
	// Copy the mutable payload map defensively: the job is processed off the
	// request goroutine, and callers may mutate their payload map afterwards.
	payloadCopy := clonePayload(payload)
	job := auditJob{
		sessionID:     sessionID,
		toolName:      toolName,
		status:        result.Status,
		executionTime: time.Since(start).Milliseconds(),
		payload:       payloadCopy,
		result:        result,
	}
	if s.audit != nil {
		s.audit.Submit(job)
	} else {
		s.persistAuditSync(ctx, job)
	}

	// SEC-18: broadcast only tool name + status.
	s.bus.Publish("mcp_tool_executed", map[string]interface{}{"tool": result.Tool, "status": result.Status})
	return result, nil
}

// persistAuditSync is the fallback path when no AuditPool is wired (unit tests).
// Mirrors AuditPool.persist so the synchronous audit trail stays identical.
func (s *MCPService) persistAuditSync(ctx context.Context, job auditJob) {
	payloadJSON, _ := json.Marshal(job.payload)
	resultJSON, _ := json.Marshal(job.result)
	toolCall := models.ToolCall{
		SessionID: job.sessionID,
		ToolName:  job.toolName,
		Payload:   string(payloadJSON),
		Result:    string(resultJSON),
		Status:    job.status,
	}
	sessionID := job.sessionID
	aiLog := models.AILog{
		SessionID:     &sessionID,
		Workflow:      "mcp_tool_execution",
		ToolName:      job.toolName,
		Status:        job.status,
		ExecutionTime: job.executionTime,
		Response:      string(resultJSON),
	}
	if err := s.repo.CreateToolCall(ctx, &toolCall); err != nil {
		auth.LogSecurity("tool_call_persist_failed", map[string]any{
			"session_id": job.sessionID.String(),
			"tool_name":  job.toolName,
			"error":      err.Error(),
		})
	}
	if err := s.repo.CreateAILog(ctx, &aiLog); err != nil {
		auth.LogSecurity("ai_log_persist_failed", map[string]any{
			"session_id": job.sessionID.String(),
			"workflow":   "mcp_tool_execution",
			"tool_name":  job.toolName,
			"error":      err.Error(),
		})
	}
}

// clonePayload returns a shallow copy of the tool payload map so the async
// audit worker is not racing with a caller that mutates its own map after
// Execute returns. Values are not deep-copied: the audit path only marshals
// them, never mutates.
func clonePayload(p map[string]interface{}) map[string]interface{} {
	if p == nil {
		return nil
	}
	c := make(map[string]interface{}, len(p))
	for k, v := range p {
		c[k] = v
	}
	return c
}

func (s *MCPService) executeSearchTrips(ctx context.Context, sessionID uuid.UUID, payload map[string]interface{}) ToolResult {
	query := getString(payload, "query")
	if query == "" {
		query = getString(payload, "prompt")
	}

	session, err := s.repo.FindChatSession(ctx, sessionID)
	if err != nil {
		log.Printf("[mcp] search_trips failed session lookup error=%v", err)
		return ToolResult{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "session not found"}}
	}

	alternative := false
	if v, ok := payload["alternative"].(string); ok {
		alternative = strings.EqualFold(v, "true") || v == "1"
	}
	if v, ok := payload["alternative"].(bool); ok {
		alternative = v
	}

	// Validator: if user already selected a package and is not explicitly asking
	// for alternatives, refuse to search to avoid recommendation spam. Include
	// the selected package title so the LLM (and the finalizeChat surfacing
	// guard) can tell the user WHICH package is selected and offer options.
	if session.SelectedTripID != nil && !alternative {
		selectedTitle := ""
		if trip, terr := s.repo.FindTrip(ctx, *session.SelectedTripID); terr == nil {
			selectedTitle = trip.Title
		}
		return ToolResult{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{
			"error":               "a package is already selected",
			"selected_trip_id":    session.SelectedTripID.String(),
			"selected_trip_title": selectedTitle,
		}}
	}

	repoQuery := repositories.TripRepositoryFilter{
		PublishedOnly: true,
		Limit:         20,
	}
	packages, err := s.repo.ListTrips(ctx, repoQuery)
	if err != nil {
		log.Printf("[mcp] search_trips failed list trips error=%v", err)
		return ToolResult{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": err.Error()}}
	}

	scored := scoreTrips(query, packages)
	if len(scored) == 0 && len(packages) > 0 {
		scored = packages[:min(3, len(packages))]
	}

	results := make([]map[string]interface{}, 0, len(scored))
	for _, trip := range scored {
		// AIW-2: Limit fields returned to LLM to prevent context window bloat.
		// AIW-1: Sanitize content strings to prevent indirect prompt injection.
		sanitizedSummary := sanitizePromptInjection(trip.Summary)
		var sanitizedHighlights []string
		for _, h := range trip.Highlights {
			sanitizedHighlights = append(sanitizedHighlights, sanitizePromptInjection(h))
		}

		// AIW-5: expose safe, relevant pricing so the LLM can answer discount /
		// child-price questions grounded in backend data (source of truth).
		// Prices reuse the same effective-price helpers as the booking flow, so
		// the catalog card quote never contradicts the charged total. `price` is
		// kept (effective adult price) for backward compatibility with the
		// frontend recommendation cards; the new fields add the full picture.
		pb := priceBreakdown(trip, 1, 0)
		results = append(results, map[string]interface{}{
			"id":          trip.ID.String(),
			"slug":        sanitizePromptInjection(trip.Slug),
			"title":       sanitizePromptInjection(trip.Title),
			"destination": sanitizePromptInjection(trip.Destination),
			"location":    sanitizePromptInjection(trip.Location),
			"category":    sanitizePromptInjection(trip.Category),
			"duration":    sanitizePromptInjection(trip.Duration),
			"summary":     limitString(sanitizedSummary, 150),
			"price":       firstNonZero(trip.BasePrice, trip.EstimatedPrice),
			// New pricing fields (effective adult price honors discount).
			"adult_price":            pb.AdultNormalPrice,
			"adult_effective_price":  pb.AdultUnitPrice,
			"child_price":            trip.ChildPrice,
			"discount_enabled":       pb.AdultDiscountEnabled,
			"discount_price":         trip.DiscountPrice,
			"child_discount_enabled": pb.ChildDiscountEnabled,
			"child_discount":         trip.ChildDiscount,
			"highlights":             limitSlice(sanitizedHighlights, 3),
			"image_url":              trip.ImageURL,
		})
	}

	reason := "initial"
	if alternative {
		reason = "alternative"
	}

	return ToolResult{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
		"packages": results,
		"count":    len(results),
		"reason":   reason,
		"query":    query,
	}}
}

func (s *MCPService) executeSelectPackage(ctx context.Context, sessionID uuid.UUID, payload map[string]interface{}) ToolResult {
	tripIDStr := getString(payload, "trip_id")
	tripID, err := uuid.Parse(tripIDStr)
	if err != nil {
		return ToolResult{Tool: mcp.ToolSelectPackage, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "invalid trip_id"}}
	}

	if _, err := s.repo.FindTrip(ctx, tripID); err != nil {
		return ToolResult{Tool: mcp.ToolSelectPackage, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "trip not found"}}
	}

	session, err := s.repo.FindChatSession(ctx, sessionID)
	if err != nil || (session.ExpiresAt != nil && !session.ExpiresAt.After(time.Now())) {
		return ToolResult{Tool: mcp.ToolSelectPackage, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "chat session expired"}}
	}
	if err := s.repo.UpdateChatSessionSelectedTrip(ctx, sessionID, &tripID); err != nil {
		log.Printf("[mcp] select_package failed update session error=%v", err)
		return ToolResult{Tool: mcp.ToolSelectPackage, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "failed to update session"}}
	}

	return ToolResult{Tool: mcp.ToolSelectPackage, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
		"success": true,
		"trip_id": tripID.String(),
	}}
}

func (s *MCPService) executeCollectOrderDetail(toolName string, payload map[string]interface{}) ToolResult {
	tripIDStr := getString(payload, "trip_id")
	tripID, err := uuid.Parse(tripIDStr)
	if err != nil {
		return ToolResult{Tool: toolName, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "invalid trip_id"}}
	}

	getDefault := func(key string, fallback string) string {
		if v := getString(payload, key); v != "" {
			return v
		}
		return fallback
	}

	detail := map[string]interface{}{
		"trip_id":       tripID.String(),
		"adult_pax":     parsePax(payload, "adult_pax", 1),
		"child_pax":     parsePax(payload, "child_pax", 0),
		"travel_date":   getDefault("travel_date", ""),
		"contact_name":  getDefault("contact_name", ""),
		"contact_email": getDefault("contact_email", ""),
		"contact_phone": getDefault("contact_phone", ""),
	}

	return ToolResult{Tool: toolName, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
		"success": true,
		"draft":   detail,
	}}
}

// resolveAITrip is a shared lookup for the AI-facing read tools (AIW-5). It
// parses trip_id from the payload and loads the trip (with itineraries
// preloaded by the repository). Returns a user/AI-safe error message when the
// id is malformed or the trip does not exist — never a raw DB error.
func (s *MCPService) resolveAITrip(ctx context.Context, payload map[string]interface{}) (models.Trip, uuid.UUID, string) {
	tripIDStr := getString(payload, "trip_id")
	tripID, err := uuid.Parse(tripIDStr)
	if err != nil {
		return models.Trip{}, uuid.Nil, "invalid trip_id"
	}
	trip, err := s.repo.FindTrip(ctx, tripID)
	if err != nil {
		return models.Trip{}, uuid.Nil, "trip not found"
	}
	return trip, tripID, ""
}

// sanitizeStringSlice applies prompt-injection sanitization to a slice of
// free-text catalog strings before they are sent to the LLM (AIW-1).
func sanitizeStringSlice(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, sanitizePromptInjection(it))
	}
	return out
}

// executeGetTripDetail returns the full, AI-safe detail of ONE package (AIW-5).
// It deliberately exposes only fields the AI needs to answer detail questions —
// no internal DB bookkeeping (CreatedAt/UpdatedAt/DeletedAt, soft-delete, raw
// publish scheduling internals). All free-text is sanitized (AIW-1) and prices
// reuse the booking pricing helpers so the AI never contradicts the backend.
func (s *MCPService) executeGetTripDetail(ctx context.Context, payload map[string]interface{}) ToolResult {
	trip, tripID, errMsg := s.resolveAITrip(ctx, payload)
	if errMsg != "" {
		return ToolResult{Tool: mcp.ToolGetTripDetail, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": errMsg}}
	}

	pb := priceBreakdown(trip, 1, 0)

	// Itinerary (daily plan), sanitized + ordered by day.
	itinerary := make([]map[string]interface{}, 0, len(trip.Itineraries))
	for _, it := range trip.Itineraries {
		itinerary = append(itinerary, map[string]interface{}{
			"day":         it.Day,
			"title":       sanitizePromptInjection(it.Title),
			"description": sanitizePromptInjection(it.Description),
		})
	}

	// Media gallery (URL + type + alt text only).
	media := make([]map[string]interface{}, 0, len(trip.Media))
	for _, m := range trip.Media {
		media = append(media, map[string]interface{}{
			"url":      m.URL,
			"type":     m.Type,
			"alt_text": sanitizePromptInjection(m.AltText),
		})
	}

	data := map[string]interface{}{
		"id":          tripID.String(),
		"slug":        sanitizePromptInjection(trip.Slug),
		"title":       sanitizePromptInjection(trip.Title),
		"destination": sanitizePromptInjection(trip.Destination),
		"location":    sanitizePromptInjection(trip.Location),
		"category":    sanitizePromptInjection(trip.Category),
		"status":      trip.Status,
		"duration":    sanitizePromptInjection(trip.Duration),
		"overview":    sanitizePromptInjection(trip.Overview),
		"summary":     sanitizePromptInjection(trip.Summary),
		// Pricing (source of truth = booking helpers).
		"adult_price":            pb.AdultNormalPrice,
		"adult_effective_price":  pb.AdultUnitPrice,
		"child_price":            trip.ChildPrice,
		"discount_enabled":       pb.AdultDiscountEnabled,
		"discount_price":         trip.DiscountPrice,
		"child_discount_enabled": pb.ChildDiscountEnabled,
		"child_discount":         trip.ChildDiscount,
		// Capacity (default pax quota configured on the package).
		"adult_pax_quota": trip.AdultPax,
		"child_pax_quota": trip.ChildPax,
		// Rich detail.
		"highlights":         sanitizeStringSlice(trip.Highlights),
		"amenities_included": sanitizeStringSlice(trip.AmenitiesIncluded),
		"amenities_excluded": sanitizeStringSlice(trip.AmenitiesExcluded),
		"references":         sanitizeStringSlice(trip.References),
		"itinerary":          itinerary,
		"media":              media,
		"image_url":          trip.ImageURL,
	}
	// Travel window (only when configured) so the AI can reason about dates.
	if trip.PackageStartDate != nil {
		data["package_start_date"] = trip.PackageStartDate.Format("2006-01-02")
	}
	if trip.PackageEndDate != nil {
		data["package_end_date"] = trip.PackageEndDate.Format("2006-01-02")
	}

	return ToolResult{Tool: mcp.ToolGetTripDetail, Status: models.ToolResultStatusSuccess, Data: data}
}

// executeCalculateTripPrice returns the authoritative price quote for a given
// pax mix (AIW-5). The LLM must never compute a total itself; it calls this and
// reports breakdown.Total. Because priceBreakdown is the same helper used by
// BookingService.Create, the quoted total equals the total charged at booking.
func (s *MCPService) executeCalculateTripPrice(ctx context.Context, payload map[string]interface{}) ToolResult {
	trip, tripID, errMsg := s.resolveAITrip(ctx, payload)
	if errMsg != "" {
		return ToolResult{Tool: mcp.ToolCalculateTripPrice, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": errMsg}}
	}

	adultPax := parsePax(payload, "adult_pax", 1)
	childPax := parsePax(payload, "child_pax", 0)
	// Enforce the same server-side pax bounds as booking (SEC-11) so a quote can
	// never be produced for an impossible/absurd pax count.
	if adultPax < 0 || childPax < 0 || adultPax > dto.MaxBookingPax || childPax > dto.MaxBookingPax {
		return ToolResult{Tool: mcp.ToolCalculateTripPrice, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "pax must be between 0 and " + strconv.Itoa(dto.MaxBookingPax)}}
	}
	if adultPax <= 0 && childPax <= 0 {
		adultPax = 1
	}

	pb := priceBreakdown(trip, adultPax, childPax)
	return ToolResult{Tool: mcp.ToolCalculateTripPrice, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
		"trip_id":                tripID.String(),
		"title":                  sanitizePromptInjection(trip.Title),
		"currency":               "IDR",
		"adult_normal_price":     pb.AdultNormalPrice,
		"adult_discount_enabled": pb.AdultDiscountEnabled,
		"adult_discount_price":   pb.AdultDiscountPrice,
		"adult_unit_price":       pb.AdultUnitPrice,
		"adult_pax":              pb.AdultPax,
		"adult_subtotal":         pb.AdultSubtotal,
		"child_normal_price":     pb.ChildNormalPrice,
		"child_discount_enabled": pb.ChildDiscountEnabled,
		"child_discount_price":   pb.ChildDiscountPrice,
		"child_unit_price":       pb.ChildUnitPrice,
		"child_pax":              pb.ChildPax,
		"child_subtotal":         pb.ChildSubtotal,
		"total":                  pb.Total,
	}}
}

// executeCheckTripAvailability verifies whether a package can be booked for a
// given travel date + pax count, derived from backend catalog data (source of
// truth): publish status, package travel window, and the configured default pax
// quota. The platform has no per-date slot/inventory table, so this is a
// best-effort verification from catalog truth — NOT a guess. When availability
// cannot be positively confirmed from catalog data, it returns
// availability_confirmed=false so the AI tells the user it needs verification
// instead of over-promising.
func (s *MCPService) executeCheckTripAvailability(ctx context.Context, payload map[string]interface{}) ToolResult {
	trip, tripID, errMsg := s.resolveAITrip(ctx, payload)
	if errMsg != "" {
		return ToolResult{Tool: mcp.ToolCheckTripAvailability, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": errMsg}}
	}

	adultPax := parsePax(payload, "adult_pax", 1)
	childPax := parsePax(payload, "child_pax", 0)
	if adultPax < 0 || childPax < 0 || adultPax > dto.MaxBookingPax || childPax > dto.MaxBookingPax {
		return ToolResult{Tool: mcp.ToolCheckTripAvailability, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "invalid pax count"}}
	}
	if adultPax <= 0 && childPax <= 0 {
		adultPax = 1
	}

	travelDateStr := getString(payload, "travel_date")
	travelDate := parseDate(travelDateStr)
	if travelDate == nil {
		return ToolResult{Tool: mcp.ToolCheckTripAvailability, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "invalid travel_date format, please use ISO format (YYYY-MM-DD)"}}
	}

	// Collect blocking reasons. Each is a concrete, backend-verifiable fact.
	reasons := []string{}
	if trip.Status != "published" {
		reasons = append(reasons, "package is not currently published")
	}
	// Travel window check (only when a window is configured).
	if trip.PackageStartDate != nil && travelDate.Before(*trip.PackageStartDate) {
		reasons = append(reasons, "travel_date is before the package start date ("+trip.PackageStartDate.Format("2006-01-02")+")")
	}
	if trip.PackageEndDate != nil && travelDate.After(*trip.PackageEndDate) {
		reasons = append(reasons, "travel_date is after the package end date ("+trip.PackageEndDate.Format("2006-01-02")+")")
	}
	// Quota check against configured default pax (0 = no explicit cap configured).
	if trip.AdultPax > 0 && adultPax > trip.AdultPax {
		reasons = append(reasons, "adult_pax exceeds the package quota")
	}
	if trip.ChildPax > 0 && childPax > trip.ChildPax {
		reasons = append(reasons, "child_pax exceeds the package quota")
	}

	available := len(reasons) == 0
	data := map[string]interface{}{
		"trip_id":     tripID.String(),
		"title":       sanitizePromptInjection(trip.Title),
		"travel_date": travelDate.Format("2006-01-02"),
		"adult_pax":   adultPax,
		"child_pax":   childPax,
		"available":   available,
		// availability_confirmed signals whether the backend can positively
		// confirm from catalog truth. Without a per-date inventory table we can
		// confirm "not blocked" but the AI should still phrase availability as
		// subject to final confirmation, not a hard guarantee.
		"availability_confirmed": available,
		"adult_pax_quota":        trip.AdultPax,
		"child_pax_quota":        trip.ChildPax,
	}
	if trip.PackageStartDate != nil {
		data["package_start_date"] = trip.PackageStartDate.Format("2006-01-02")
	}
	if trip.PackageEndDate != nil {
		data["package_end_date"] = trip.PackageEndDate.Format("2006-01-02")
	}
	if !available {
		data["reasons"] = reasons
	} else {
		data["note"] = "Available based on catalog schedule and quota. Final availability is confirmed when the booking is created."
	}

	return ToolResult{Tool: mcp.ToolCheckTripAvailability, Status: models.ToolResultStatusSuccess, Data: data}
}

// orderMarkerPrefix tags the system ChatMessage written after a successful
// create_booking so the order can be found again for THIS session (AIW-8). The
// bookings table has no session_id column, so the linkage is stored as a
// marker message on the chat session (chat_messages.session_id already exists
// and is indexed) — no schema change required.
const orderMarkerPrefix = "__order_created__:"

// orderMarker holds the fields persisted into the marker message content as
// JSON after the prefix. PII is kept minimal (contact name only); email/phone
// are deliberately NOT stored here because chat messages are replayed to the
// LLM as conversation history.
type orderMarker struct {
	BookingID     string  `json:"booking_id"`
	BookingStatus string  `json:"booking_status"`
	PaymentStatus string  `json:"payment_status"`
	TotalPrice    float64 `json:"total_price"`
	ContactName   string  `json:"contact_name,omitempty"`
}

// findSessionOrder scans recent chat messages for an order marker (AIW-8).
// Returns nil when no order has been created in this session yet.
func (s *MCPService) findSessionOrder(ctx context.Context, sessionID uuid.UUID) *orderMarker {
	msgs, err := s.repo.ListRecentChatMessages(ctx, sessionID, 200)
	if err != nil {
		return nil
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		c := msgs[i].Content
		if !strings.HasPrefix(c, orderMarkerPrefix) {
			continue
		}
		var m orderMarker
		if json.Unmarshal([]byte(strings.TrimPrefix(c, orderMarkerPrefix)), &m) == nil && m.BookingID != "" {
			return &m
		}
	}
	return nil
}

// executeCheckOrderStatus reports whether an order already exists in THIS chat
// session (AIW-8). Session-scoped: it never touches other sessions' orders.
// Lets the AI answer "is my order ready / what's my order id" from backend
// truth instead of guessing, and feeds create_booking's duplicate guard.
func (s *MCPService) executeCheckOrderStatus(ctx context.Context, sessionID uuid.UUID) ToolResult {
	m := s.findSessionOrder(ctx, sessionID)
	if m == nil {
		return ToolResult{Tool: mcp.ToolCheckOrderStatus, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
			"order_exists": false,
			"note":         "No order has been created in this chat session yet.",
		}}
	}
	return ToolResult{Tool: mcp.ToolCheckOrderStatus, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
		"order_exists":   true,
		"order_id":       m.BookingID,
		"booking_id":     m.BookingID,
		"booking_status": m.BookingStatus,
		"payment_status": m.PaymentStatus,
		"total_price":    m.TotalPrice,
		"note":           "An order was already created in this session. Do NOT create another one for the same request.",
	}}
}

func scoreTrips(query string, packages []models.Trip) []models.Trip {
	if len(packages) == 0 {
		return nil
	}
	if query == "" {
		return packages[:min(3, len(packages))]
	}

	query = strings.ToLower(strings.TrimSpace(query))
	type scoredTrip struct {
		trip  models.Trip
		score int
	}
	scored := make([]scoredTrip, 0, len(packages))
	for _, trip := range packages {
		score := 0
		for _, token := range []string{trip.Title, trip.Destination, trip.Location, trip.Category, trip.Slug} {
			token = strings.ToLower(strings.TrimSpace(token))
			if token != "" && strings.Contains(query, token) {
				score += 3
			}
			if token != "" && strings.Contains(token, query) {
				score += 1
			}
		}
		for _, highlight := range trip.Highlights {
			highlight = strings.ToLower(strings.TrimSpace(highlight))
			if highlight != "" && strings.Contains(query, highlight) {
				score++
			}
		}
		scored = append(scored, scoredTrip{trip: trip, score: score})
	}

	// PERF-2: gunakan sort.SliceStable (O(N log N)) alih-alih Bubble Sort O(N^2).
	// Stabil agar urutan asli dari DB dipertahankan saat score seri (tie-break deterministik).
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Return up to 3 packages, prioritizing those with a positive score.
	// Unlike the previous break-on-zero behaviour, packages with score 0 are
	// still included (after the matching ones) so customers see every
	// available option when the catalog is small. The stable sort keeps
	// deterministic DB order among equal scores.
	// min() is the Go 1.21+ built-in (the local duplicate helper was removed).
	result := make([]models.Trip, 0, min(3, len(scored)))
	for _, item := range scored {
		result = append(result, item.trip)
		if len(result) == 3 {
			break
		}
	}
	return result
}

func parsePax(payload map[string]interface{}, key string, fallback int) int {
	switch v := payload[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		return ParseIntFromString(v, fallback)
	}
	return fallback
}

func (s *MCPService) mock(toolName string, _ map[string]any) ToolResult {
	switch toolName {
	case mcp.ToolSendWhatsApp:
		return ToolResult{Tool: toolName, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
			"delivered": true,
			"channel":   "whatsapp",
		}}
	default:
		return ToolResult{Tool: toolName, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"error": "unknown tool"}}
	}
}

func (s *MCPService) executeCreateBooking(ctx context.Context, sessionID uuid.UUID, payload map[string]interface{}) ToolResult {
	log.Printf("[mcp] create_booking called args=%+v", payload)

	// AIW-8 duplicate-order guard: if an order already exists for THIS session,
	// refuse to create a second one and return the existing order instead. This
	// prevents double orders when the user re-confirms ("lanjut") after a
	// successful create_booking.
	if existing := s.findSessionOrder(ctx, sessionID); existing != nil {
		log.Printf("[mcp] create_booking blocked duplicate session=%s existing_booking=%s", sessionID, existing.BookingID)
		return ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{
			"success":        false,
			"error":          "an order already exists for this chat session",
			"order_exists":   true,
			"order_id":       existing.BookingID,
			"booking_status": existing.BookingStatus,
			"total_price":    existing.TotalPrice,
		}}
	}

	guestUser, err := s.auth.GuestUser(ctx)
	if err != nil {
		log.Printf("[mcp] create_booking failed guest_user error=%v", err)
		return ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"success": false, "error": err.Error()}}
	}

	tripIDStr, _ := payload["trip_id"].(string)
	tripID, err := uuid.Parse(tripIDStr)
	if err != nil {
		log.Printf("[mcp] create_booking failed invalid_trip_id trip_id=%q", tripIDStr)
		return ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"success": false, "error": "invalid trip_id"}}
	}

	req := dto.BookingRequest{
		TripID:       tripID,
		AdultPax:     parsePax(payload, "adult_pax", 1),
		ChildPax:     parsePax(payload, "child_pax", 0),
		ContactName:  getString(payload, "contact_name"),
		ContactEmail: getString(payload, "contact_email"),
		ContactPhone: getString(payload, "contact_phone"),
		TravelDate:   getString(payload, "travel_date"),
	}

	if req.ContactName == "" {
		req.ContactName = "Guest"
	}
	if req.ContactEmail == "" && req.ContactPhone == "" {
		log.Printf("[mcp] create_booking failed missing_contact trip_id=%s", tripID)
		return ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"success": false, "error": "contact_email or contact_phone is required"}}
	}

	log.Printf("[mcp] create_booking saving trip_id=%s adult_pax=%d child_pax=%d contact_email=%q contact_phone=%q travel_date=%q", req.TripID, req.AdultPax, req.ChildPax, req.ContactEmail, req.ContactPhone, req.TravelDate)
	// BUG-9: Validate that travel_date parses successfully before booking.
	parsedDate := parseDate(req.TravelDate)
	if parsedDate == nil {
		log.Printf("[mcp] create_booking failed invalid_date travel_date=%q", req.TravelDate)
		return ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"success": false, "error": "invalid travel_date format, please use ISO format (YYYY-MM-DD)"}}
	}

	booking, err := s.bookings.Create(ctx, guestUser.ID, req)
	if err != nil {
		log.Printf("[mcp] create_booking save failed error=%v", err)
		return ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{"success": false, "error": err.Error()}}
	}
	log.Printf("[mcp] create_booking saved booking_id=%s status=%s payment_status=%s total=%.2f", booking.ID, booking.BookingStatus, booking.PaymentStatus, booking.TotalPrice)

	// AIW-8: persist an order marker on the chat session so check_order_status
	// and the duplicate guard can find this order later. Best-effort: a marker
	// write failure does not fail the already-created booking. The marker stores
	// only non-PII identifiers + total (contact name at most); email/phone are
	// deliberately excluded because chat history is replayed to the LLM.
	marker := orderMarker{
		BookingID:     booking.ID.String(),
		BookingStatus: booking.BookingStatus,
		PaymentStatus: booking.PaymentStatus,
		TotalPrice:    booking.TotalPrice,
		ContactName:   req.ContactName,
	}
	if mj, merr := json.Marshal(marker); merr == nil {
		if aerr := s.repo.AddChatMessage(ctx, &models.ChatMessage{SessionID: sessionID, Role: "system", Content: orderMarkerPrefix + string(mj)}); aerr != nil {
			log.Printf("[mcp] create_booking order marker persist failed session=%s booking=%s err=%v", sessionID, booking.ID, aerr)
		}
	}

	return ToolResult{Tool: mcp.ToolCreateBooking, Status: models.ToolResultStatusSuccess, Data: map[string]interface{}{
		"success":        true,
		"order_id":       booking.ID.String(),
		"status":         booking.BookingStatus,
		"booking_id":     booking.ID.String(),
		"booking_status": booking.BookingStatus,
		"payment_status": booking.PaymentStatus,
		"total_price":    booking.TotalPrice,
	}}
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
