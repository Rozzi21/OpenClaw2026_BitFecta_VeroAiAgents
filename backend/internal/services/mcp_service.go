package services

import (
	"context"
	"encoding/json"
	"log"
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
type MCPService struct {
	repo     MCPRepository
	bus      *events.Bus
	bookings BookingCreator
	auth     GuestUserProvider
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

	case mcp.ToolSearchTrips, "search_destination", "search_hotels", "calculate_budget", "generate_itinerary":
		// Unify legacy recommendation-like calls into search_trips behavior.
		result = s.executeSearchTrips(ctx, sessionID, payload)

	case mcp.ToolSelectPackage:
		result = s.executeSelectPackage(ctx, sessionID, payload)

	case mcp.ToolCollectOrderDetail, mcp.ToolUpdateOrderDraft:
		result = s.executeCollectOrderDetail(toolName, payload)

	case mcp.ToolCreateBooking, mcp.ToolCreateOrder:
		result = s.executeCreateBooking(ctx, payload)

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

	payloadJSON, _ := json.Marshal(payload)
	resultJSON, _ := json.Marshal(result)
	toolCall := models.ToolCall{
		SessionID: sessionID,
		ToolName:  toolName,
		Payload:   string(payloadJSON),
		Result:    string(resultJSON),
		Status:    result.Status,
	}
	aiLog := models.AILog{
		SessionID:     &sessionID,
		Workflow:      "mcp_tool_execution",
		ToolName:      toolName,
		Status:        result.Status,
		ExecutionTime: time.Since(start).Milliseconds(),
		Response:      string(resultJSON),
	}
	// SEC-21: Persist tool call + AI log asynchronously, but bounded/rate-limited via standard means.
	// We'll keep it simple: synchronous. Bounding goroutines here is safer to prevent flood.
	if err := s.repo.CreateToolCall(ctx, &toolCall); err != nil {
		auth.LogSecurity("tool_call_persist_failed", map[string]any{
			"session_id": sessionID.String(),
			"tool_name":  toolName,
			"error":      err.Error(),
		})
	}
	if err := s.repo.CreateAILog(ctx, &aiLog); err != nil {
		auth.LogSecurity("ai_log_persist_failed", map[string]any{
			"session_id": sessionID.String(),
			"workflow":   "mcp_tool_execution",
			"tool_name":  toolName,
			"error":      err.Error(),
		})
	}

	// SEC-18: broadcast only tool name + status.
	s.bus.Publish("mcp_tool_executed", map[string]interface{}{"tool": result.Tool, "status": result.Status})
	return result, nil
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
	// for alternatives, refuse to search to avoid recommendation spam.
	if session.SelectedTripID != nil && !alternative {
		return ToolResult{Tool: mcp.ToolSearchTrips, Status: models.ToolResultStatusFailed, Data: map[string]interface{}{
			"error":               "a package is already selected",
			"selected_trip_id":    session.SelectedTripID.String(),
			"require_alternative": true,
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

		results = append(results, map[string]interface{}{
			"id":          trip.ID.String(),
			"title":       sanitizePromptInjection(trip.Title),
			"destination": sanitizePromptInjection(trip.Destination),
			"location":    sanitizePromptInjection(trip.Location),
			"category":    sanitizePromptInjection(trip.Category),
			"duration":    sanitizePromptInjection(trip.Duration),
			"summary":     limitString(sanitizedSummary, 150),
			"price":       firstNonZero(trip.BasePrice, trip.EstimatedPrice),
			"highlights":  limitSlice(sanitizedHighlights, 3),
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

	for i := 0; i < len(scored)-1; i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[i].score < scored[j].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	result := make([]models.Trip, 0, 3)
	for _, item := range scored {
		if item.score == 0 && len(result) > 0 {
			break
		}
		result = append(result, item.trip)
		if len(result) == 3 {
			break
		}
	}
	return result
}

func parsePax(payload map[string]interface{}, key string, fallback int) int {
	if v, ok := payload[key].(float64); ok {
		return int(v)
	}
	if v, ok := payload[key].(string); ok {
		return parseIntFallback(v, fallback)
	}
	return fallback
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

func (s *MCPService) executeCreateBooking(ctx context.Context, payload map[string]interface{}) ToolResult {
	log.Printf("[mcp] create_booking called args=%+v", payload)
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

func parseIntFallback(v string, fallback int) int {
	return ParseIntFromString(v, fallback)
}
