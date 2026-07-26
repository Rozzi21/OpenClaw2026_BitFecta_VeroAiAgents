package services

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/ai"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/mcp"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

type AIService struct {
	repo   *repositories.Repository
	mcp    *MCPService
	bus    *events.Bus
	client *ai.Client
	cfg    config.Config
}

type ChatResult struct {
	SessionID            uuid.UUID     `json:"-"`
	Message              string        `json:"message"`
	Workflow             []ToolResult  `json:"workflow"`
	ShowRecommendations  bool          `json:"show_recommendations"`
	RecommendationReason string        `json:"recommendation_reason"`
	RecommendedPackages  []models.Trip `json:"recommended_packages"`
}

// CleanupExpiredChatSessions is intentionally a small service operation so an
// in-process ticker can be replaced by cron/systemd/Kubernetes later without
// duplicating cleanup SQL outside the repository.
func (s *AIService) CleanupExpiredChatSessions(now time.Time) (int64, error) {
	return s.repo.DeleteExpiredChatSessions(now)
}

func (s *AIService) Chat(chatCtx ChatContext, req dto.ChatRequest) (ChatResult, error) {
	sessionID := chatCtx.SessionID
	if sessionID == uuid.Nil {
		return ChatResult{}, errors.New("chat session is required")
	}

	session, err := s.repo.FindChatSession(sessionID)
	if err != nil {
		return ChatResult{}, err
	}
	if !sessionOwnedByContext(session, chatCtx) {
		return ChatResult{}, errors.New("chat session not found")
	}
	now := time.Now()
	if session.ExpiresAt != nil && !session.ExpiresAt.After(now) {
		return ChatResult{}, errors.New("chat session expired")
	}
	if session.ExpiresAt == nil {
		expiresAt := now.Add(s.cfg.GuestSessionTTL)
		session.ExpiresAt = &expiresAt
	}
	session.LastActivityAt = &now
	if err := s.repo.UpdateChatSessionActivity(session.ID, *session.ExpiresAt, now); err != nil {
		return ChatResult{}, err
	}

	if err := s.repo.AddChatMessage(&models.ChatMessage{SessionID: sessionID, Role: "user", Content: req.Prompt}); err != nil {
		return ChatResult{}, err
	}

	// Use tool-driven workflow. The LLM decides whether to call search_trips,
	// select_package, collect_order_detail, or create_booking.
	aiResponse, toolResults, err := s.generateWithToolLoop(sessionID, req.Prompt)
	response := "Maaf, saya belum bisa memproses permintaan Anda saat ini. Silakan coba lagi."
	if err != nil {
		errorPayload, _ := json.Marshal(map[string]interface{}{
			"error": err.Error(),
			"mode":  "local_fallback",
		})
		_ = s.repo.CreateAILog(&models.AILog{
			SessionID: &sessionID,
			Workflow:  "ai_generation",
			Status:    "failed",
			Response:  string(errorPayload),
		})
	} else if aiResponse.Text != "" {
		response = aiResponse.Text
		payload, _ := json.Marshal(aiResponse.Metadata)
		_ = s.repo.CreateAILog(&models.AILog{
			SessionID: &sessionID,
			Workflow:  "ai_generation",
			Status:    "success",
			Response:  string(payload),
		})
		s.bus.Publish("ai_response", map[string]interface{}{
			"session_id": sessionID,
			"status":     aiResponse.RawStatus,
		})
	}

	// Defense-in-depth: model must not claim booking success unless tool succeeded.
	if responseClaimsOrderCreated(response) && !hasSuccessfulCreateBooking(toolResults) {
		log.Printf("[ai] blocked unsafe booking success claim for session=%s", sessionID)
		response = "Maaf, saya belum berhasil membuat pesanan Anda karena terjadi kendala pada sistem. Silakan coba beberapa saat lagi."
	}

	// Refresh selected trip in case this process loaded a stale session pointer.
	chatSession, _ := s.repo.FindChatSession(sessionID)
	selectedTripID := chatSession.SelectedTripID

	// Compute recommendation control based solely on tool results.
	showRecommendations := false
	recommendationReason := ""
	recommendedPackages := extractRecommendedPackages(toolResults, selectedTripID)

	if len(recommendedPackages) > 0 {
		showRecommendations = true
		recommendationReason = recommendationReasonFromToolResults(toolResults)
		if recommendationReason == "" {
			recommendationReason = "initial"
		}
	}

	if selectedTripID != nil && !hasSearchTripsAlternative(toolResults) {
		showRecommendations = false
		recommendationReason = ""
		recommendedPackages = nil
	}

	if hasSuccessfulCreateBooking(toolResults) {
		showRecommendations = false
		recommendationReason = ""
		recommendedPackages = nil
	}

	if err := s.repo.AddChatMessage(&models.ChatMessage{SessionID: sessionID, Role: "assistant", Content: response}); err != nil {
		return ChatResult{}, err
	}
	_ = s.refreshMemorySummary(sessionID)

	// SEC-18: broadcast only session_id as completion signal.
	s.bus.Publish("workflow_completed", map[string]interface{}{"session_id": sessionID})

	return ChatResult{
		SessionID:            sessionID,
		Message:              response,
		Workflow:             toolResults,
		ShowRecommendations:  showRecommendations,
		RecommendationReason: recommendationReason,
		RecommendedPackages:  recommendedPackages,
	}, nil
}

func sessionOwnedByContext(session models.ChatSession, chatCtx ChatContext) bool {
	if chatCtx.UserID == nil {
		return session.UserID == nil
	}
	return session.UserID != nil && *session.UserID == *chatCtx.UserID
}

func extractRecommendedPackages(toolResults []ToolResult, selectedTripID *uuid.UUID) []models.Trip {
	for _, result := range toolResults {
		if result.Tool == mcp.ToolSearchTrips && result.Status == "success" {
			data, ok := result.Data["packages"].([]map[string]interface{})
			if !ok {
				return nil
			}
			packages := make([]models.Trip, 0, len(data))
			for _, item := range data {
				trip := models.Trip{}
				if idStr, ok := item["id"].(string); ok {
					if id, err := uuid.Parse(idStr); err == nil {
						trip.ID = id
					}
				}
				if title, ok := item["title"].(string); ok {
					trip.Title = title
				}
				if slug, ok := item["slug"].(string); ok {
					trip.Slug = slug
				}
				if destination, ok := item["destination"].(string); ok {
					trip.Destination = destination
				}
				if location, ok := item["location"].(string); ok {
					trip.Location = location
				}
				if category, ok := item["category"].(string); ok {
					trip.Category = category
				}
				if duration, ok := item["duration"].(string); ok {
					trip.Duration = duration
				}
				if summary, ok := item["summary"].(string); ok {
					trip.Summary = summary
				}
				if v, ok := item["price"].(float64); ok {
					trip.BasePrice = v
				}
				if highlights, ok := item["highlights"].([]string); ok {
					trip.Highlights = highlights
				}
				if imageURL, ok := item["image_url"].(string); ok {
					trip.ImageURL = imageURL
				}
				packages = append(packages, trip)
			}

			// If a package is already selected, do not send packages that are
			// unrelated. However, if user asked for alternatives, allow them.
			if selectedTripID != nil && !hasSearchTripsAlternative(toolResults) {
				for _, trip := range packages {
					if trip.ID == *selectedTripID {
						return []models.Trip{trip}
					}
				}
				return nil
			}
			return packages
		}
	}
	return nil
}

func recommendationReasonFromToolResults(toolResults []ToolResult) string {
	for _, result := range toolResults {
		if result.Tool == mcp.ToolSearchTrips && result.Status == "success" {
			if reason, ok := result.Data["reason"].(string); ok {
				return reason
			}
		}
	}
	return ""
}

func hasSearchTripsAlternative(toolResults []ToolResult) bool {
	for _, result := range toolResults {
		if result.Tool == mcp.ToolSearchTrips && result.Status == "success" {
			if reason, ok := result.Data["reason"].(string); ok && reason == "alternative" {
				return true
			}
		}
	}
	return false
}

func hasSuccessfulCreateBooking(results []ToolResult) bool {
	for _, result := range results {
		if (result.Tool == mcp.ToolCreateBooking || result.Tool == mcp.ToolCreateOrder) && result.Status == "success" {
			if success, ok := result.Data["success"].(bool); ok && success {
				return true
			}
		}
	}
	return false
}

func responseClaimsOrderCreated(response string) bool {
	lower := strings.ToLower(response)
	if strings.Contains(lower, "belum berhasil") || strings.Contains(lower, "tidak berhasil") || strings.Contains(lower, "gagal") {
		return false
	}
	phrases := []string{
		"pesanan anda berhasil dibuat",
		"pesanan anda sudah dibuat",
		"pesanan anda telah dibuat",
		"pesanan sudah berhasil dibuat",
		"pesanan berhasil dibuat",
		"pemesanan anda berhasil",
		"pemesanan berhasil",
		"booking anda berhasil",
		"reservasi anda berhasil",
		"order has been successfully created",
		"order successfully created",
		"order anda berhasil",
		"booking has been successfully created",
		"booking successfully created",
		"berhasil saya buatkan",
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	orderWords := []string{"pesanan", "pemesanan", "order", "booking", "reservasi"}
	successWords := []string{"berhasil dibuat", "sudah dibuat", "telah dibuat", "successfully created", "created successfully"}
	for _, orderWord := range orderWords {
		if !strings.Contains(lower, orderWord) {
			continue
		}
		for _, successWord := range successWords {
			if strings.Contains(lower, successWord) {
				return true
			}
		}
	}
	return false
}

// generateWithToolLoop calls the LLM with OpenAI function calling enabled.
// If the LLM responds with tool_calls, this function executes them via MCP,
// appends the results back into the conversation, and calls the LLM again
// so it can generate a final text response based on actual tool results.
func (s *AIService) generateWithToolLoop(sessionID uuid.UUID, prompt string) (ai.CompletionResponse, []ToolResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.AITimeout)
	defer cancel()

	messages := s.buildMessages(sessionID, prompt)
	tools := mcp.OpenAITools()

	var allToolResults []ToolResult

	for round := 0; round < ai.MaxToolCallRounds; round++ {
		resp, err := s.client.Generate(ctx, ai.CompletionRequest{
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			return resp, allToolResults, err
		}

		if len(resp.ToolCalls) == 0 {
			return resp, allToolResults, nil
		}

		log.Printf("[ai] round %d: LLM requested %d tool call(s)", round+1, len(resp.ToolCalls))

		assistantMsg := ai.Message{
			Role:      "assistant",
			ToolCalls: resp.ToolCalls,
		}
		messages = append(messages, assistantMsg)

		for _, tc := range resp.ToolCalls {
			log.Printf("[ai] executing tool: %s (call_id=%s) args=%s", tc.Function.Name, tc.ID, tc.Function.Arguments)

			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				log.Printf("[ai] failed to parse tool args for %s: %v", tc.Function.Name, err)
				args = map[string]interface{}{}
			}

			toolResult, execErr := s.mcp.Execute(sessionID, tc.Function.Name, args)
			if execErr != nil {
				log.Printf("[ai] tool execution error for %s: %v", tc.Function.Name, execErr)
				toolResult = ToolResult{
					Tool:   tc.Function.Name,
					Status: "failed",
					Data:   map[string]interface{}{"error": execErr.Error()},
				}
			}

			allToolResults = append(allToolResults, toolResult)

			resultJSON, _ := json.Marshal(toolResult)
			log.Printf("[ai] tool result for %s: %s", tc.Function.Name, string(resultJSON))

			messages = append(messages, ai.Message{
				Role:       "tool",
				Content:    string(resultJSON),
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	log.Printf("[ai] exhausted %d tool call rounds, forcing final text response", ai.MaxToolCallRounds)
	resp, err := s.client.Generate(ctx, ai.CompletionRequest{Messages: messages})
	return resp, allToolResults, err
}

func (s *AIService) buildMessages(sessionID uuid.UUID, prompt string) []ai.Message {
	messages := []ai.Message{
		{
			Role: "system",
			Content: "You are Vero Travel, a professional travel assistant. Answer in natural Indonesian. " +
				"You are helping a customer plan and book a travel package. " +
				"Use the tool `search_trips(query, alternative) ONLY when the user is looking for package recommendations, searching for destinations, or explicitly asks for alternatives. Do not call search_trips before every response. " +
				"Once the user has selected a package (via `select_package(trip_id)`), focus on collecting booking details: number of adults, number of children, travel date, and contact info (email or WhatsApp). " +
				"Call `collect_order_detail` when gathering missing info. It does NOT create an order. " +
				"Only call `create_booking` after ALL required info is collected. " +
				"NEVER tell the customer the order is created until `create_booking` returns success. " +
				"If `create_booking` fails, apologize and ask them to try again. " +
				"Use natural, customer-facing language. NEVER expose internal order statuses or admin processes. " +
				"Payments are temporarily disabled, so never mention DOKU, QRIS, virtual accounts, checkout links, or payment. " +
				"Do not use Markdown formatting, bold markers, asterisks, headings, or decorative symbols. Use plain text and simple hyphen bullets only when a list is helpful.",
		},
	}

	chatSession, err := s.repo.FindChatSession(sessionID)
	if err == nil && chatSession.MemorySummary != "" {
		messages = append(messages, ai.Message{Role: "system", Content: "Conversation memory summary: " + chatSession.MemorySummary})
	}

	recent, _ := s.repo.ListRecentChatMessages(sessionID, s.cfg.AIRecentMessages)
	for _, message := range recent {
		messages = append(messages, ai.Message{Role: message.Role, Content: message.Content})
	}
	if len(recent) == 0 {
		messages = append(messages, ai.Message{Role: "user", Content: prompt})
	}

	return messages
}

func (s *AIService) refreshMemorySummary(sessionID uuid.UUID) error {
	count, err := s.repo.CountChatMessages(sessionID)
	if err != nil || count < int64(s.cfg.AIMemorySummaryAfter) {
		return err
	}
	session, err := s.repo.FindChatSession(sessionID)
	if err != nil {
		return err
	}
	tailLimit := s.cfg.AIMemoryMaxChars / 200
	if tailLimit < 20 {
		tailLimit = 20
	}
	messages, err := s.repo.TailChatMessages(sessionID, tailLimit)
	if err != nil {
		return err
	}
	var parts []string
	for _, message := range messages {
		parts = append(parts, message.Role+": "+message.Content)
	}
	summary := strings.Join(parts, "\n")
	
	// SEC-21: convert to rune slice before slicing to avoid breaking multi-byte UTF-8 chars
	runes := []rune(summary)
	if len(runes) > s.cfg.AIMemoryMaxChars {
		runes = runes[len(runes)-s.cfg.AIMemoryMaxChars:]
		summary = string(runes)
	}
	
	session.MemorySummary = summary
	return s.repo.UpdateChatSession(&session)
}
