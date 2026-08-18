package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) OpenAPI(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"openapi": "3.1.0",
		"info": gin.H{
			"title":   "Vero Travel Agents API",
			"version": "1.0.0",
			"description": "AI-native autonomous travel orchestration API. " +
				"Provides guest AI chat, public package catalog, JWT auth with refresh cookie, " +
				"trip/package management, manual pending orders, preserved-but-disabled payments (DOKU webhook), MCP tool + AI logs, " +
				"analytics, and realtime SSE events. " +
				"Temporary note: PAYMENTS_ENABLED defaults false, payment routes return 503, " +
				"and the create_payment MCP tool is intentionally disabled/unregistered from the active chat workflow.",
		},
		"servers": []gin.H{{"url": "http://localhost:8080", "description": "Local development"}},
		"tags": []gin.H{
			{"name": "Health", "description": "Service and database health"},
			{"name": "Auth", "description": "Registration, login, refresh, logout, profile"},
			{"name": "Chat", "description": "Guest AI chat workflow and chat history"},
			{"name": "Packages", "description": "Public published package catalog"},
			{"name": "Trips", "description": "Authenticated trip read + operator/admin management"},
			{"name": "Admin", "description": "Operator/admin package management, uploads, dashboard"},
			{"name": "Bookings", "description": "Booking/order creation and retrieval"},
			{"name": "Orders", "description": "Public manual order creation while payment is disabled"},
			{"name": "Payments", "description": "Preserved DOKU payment endpoints; disabled unless PAYMENTS_ENABLED=true"},
			{"name": "Logs", "description": "AI workflow logs and MCP tool calls"},
			{"name": "Analytics", "description": "Operator/admin analytics dashboard"},
			{"name": "Realtime", "description": "Server-Sent Events stream"},
		},
		"components": gin.H{
			"securitySchemes": gin.H{
				"BearerAuth": gin.H{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
					"description":  "Access token from /api/v1/auth/login or /api/v1/auth/refresh. The refresh token is delivered separately as an HttpOnly cookie.",
				},
			},
		},
		"paths": gin.H{
			// Health
			"/health":          gin.H{"get": op("Health", "Health check", false)},
			"/health/database": gin.H{"get": op("Health", "Database health check", false)},

			// Auth
			"/api/v1/auth/register": gin.H{"post": op("Auth", "Register user and issue tokens", false)},
			"/api/v1/auth/login":    gin.H{"post": op("Auth", "Login with email/username and password", false)},
			"/api/v1/auth/refresh":  gin.H{"post": op("Auth", "Refresh access token via HttpOnly refresh cookie", false)},
			"/api/v1/auth/logout":   gin.H{"post": op("Auth", "Logout and revoke refresh session", false)},
			"/api/v1/auth/me":       gin.H{"get": op("Auth", "Current authenticated user profile", true)},

			// Public packages (no auth) — consumed by the customer frontend
			"/api/v1/packages":      gin.H{"get": op("Packages", "List published packages (public)", false)},
			"/api/v1/packages/{id}": gin.H{"get": op("Packages", "Get a published package by id or slug (public)", false)},

			// Guest AI chat (no auth) — consumed by the customer frontend
			"/api/v1/chat": gin.H{"post": op("Chat", "Run the autonomous AI chat workflow as guest", false)},

			// Public manual order creation while DOKU payment is disabled.
			// Guest identity comes from the vero_guest_session HttpOnly cookie
			// (opaque random token, hashed server-side). Exactly ONE successful
			// order per guest session; a second attempt returns HTTP 403 with
			// error {status:"authentication_required", code:"GUEST_ORDER_LIMIT_REACHED"}.
			// Requires an Idempotency-Key header (16..200 chars) for safe retries.
			"/api/v1/orders":      gin.H{"post": op("Orders", "Create pending order for manual backoffice processing (guest: one order, requires Idempotency-Key header)", false)},
			"/api/v1/orders/{id}": gin.H{"get": op("Orders", "Get a guest order by id (guest cookie ownership required)", false)},

			// Authenticated chat history
			"/api/v1/chat/sessions":      gin.H{"get": op("Chat", "List chat sessions for current user", true)},
			"/api/v1/chat/{id}/messages": gin.H{"get": op("Chat", "List messages of a chat session", true)},

			// Realtime
			"/api/v1/events/stream": gin.H{"get": gin.H{
				"tags":    []string{"Realtime"},
				"summary": "Server-Sent Events stream",
				"description": "Streams workflow events: ai_response, workflow_completed, " +
					"plus mcp_tool_executed, trip_created, booking_created, booking_updated, " +
					"payment_created, payment_updated, booking_confirmed, and periodic heartbeat. " +
					"Note: payment_created/booking_confirmed only occur when PAYMENTS_ENABLED=true; current manual order flow emits booking_created only.",
				"security":  []gin.H{{"BearerAuth": []string{}}},
				"responses": okResponse("text/event-stream"),
			}},

			// Trips (authenticated read; operator/admin write)
			"/api/v1/trips":      gin.H{"get": op("Trips", "List trips", true), "post": op("Trips", "Create trip (operator/admin)", true)},
			"/api/v1/trips/{id}": gin.H{"get": op("Trips", "Get trip by id", true), "put": op("Trips", "Update trip (operator/admin)", true), "delete": op("Trips", "Delete trip (operator/admin)", true)},

			// Admin package management — consumed by the backoffice frontend
			"/api/v1/admin/packages":      gin.H{"get": op("Admin", "List packages (operator/admin)", true), "post": op("Admin", "Create package (operator/admin)", true)},
			"/api/v1/admin/packages/{id}": gin.H{"put": op("Admin", "Update package (operator/admin)", true), "delete": op("Admin", "Delete package (operator/admin)", true)},
			"/api/v1/admin/uploads":       gin.H{"post": op("Admin", "Upload trip media image (operator/admin)", true)},
			"/api/v1/admin/dashboard":     gin.H{"get": op("Admin", "Analytics dashboard (operator/admin)", true)},

			// Bookings
			"/api/v1/bookings":      gin.H{"post": op("Bookings", "Create pending booking/order", true), "get": op("Bookings", "List bookings/orders (operator/admin)", true)},
			"/api/v1/bookings/{id}": gin.H{"get": op("Bookings", "Get booking by id", true), "put": op("Bookings", "Update booking status (operator/admin)", true)},

			// Payments
			"/api/v1/payments/create":  gin.H{"post": op("Payments", "Disabled unless PAYMENTS_ENABLED=true; preserved QRIS/VA payment intent endpoint", true)},
			"/api/v1/payments/webhook": gin.H{"post": op("Payments", "Disabled unless PAYMENTS_ENABLED=true; preserved DOKU webhook", false)},
			"/api/v1/payments/{id}":    gin.H{"get": op("Payments", "Disabled unless PAYMENTS_ENABLED=true; preserved payment lookup", true)},

			// Logs (operator/admin)
			"/api/v1/logs":            gin.H{"get": op("Logs", "List AI logs (operator/admin)", true)},
			"/api/v1/logs/workflows":  gin.H{"get": op("Logs", "List workflow logs (operator/admin)", true)},
			"/api/v1/logs/tool-calls": gin.H{"get": op("Logs", "List MCP tool calls (operator/admin)", true)},

			// Analytics (operator/admin)
			"/api/v1/analytics/dashboard": gin.H{"get": op("Analytics", "Analytics dashboard (operator/admin)", true)},
		},
	})
}

func (h *Handler) ScalarDocs(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, `<!doctype html>
<html>
  <head>
    <title>Vero Travel Agents API Docs</title>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <style>body{margin:0;background:#fafafa}</style>
  </head>
  <body>
    <script id="api-reference" data-url="/openapi.json"></script>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
  </body>
</html>`)
}

func op(tag, summary string, secured bool) gin.H {
	operation := gin.H{
		"tags":      []string{tag},
		"summary":   summary,
		"responses": okResponse("application/json"),
	}
	if secured {
		operation["security"] = []gin.H{{"BearerAuth": []string{}}}
	}
	return operation
}

func okResponse(contentType string) gin.H {
	return gin.H{
		"200": gin.H{
			"description": "Successful response",
			"content": gin.H{
				contentType: gin.H{
					"schema": gin.H{
						"type": "object",
					},
				},
			},
		},
	}
}
