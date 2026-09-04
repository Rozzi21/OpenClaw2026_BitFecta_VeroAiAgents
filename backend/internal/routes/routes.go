package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/handlers"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/middlewares"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

func Register(router *gin.Engine, h *handlers.Handler, s *services.Services) {
	router.GET("/health", h.Health)
	router.GET("/health/database", h.DatabaseHealth)
	router.GET("/healthz", h.Liveness)
	router.GET("/readyz", h.Readiness)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	router.GET("/openapi.json", h.OpenAPI)
	router.GET("/docs", h.ScalarDocs)

	api := router.Group("/api/v1")
	{
		api.GET("/packages", h.PublicPackages)
		api.GET("/packages/:id", h.GetPackage)
		// SEC-13: expensive unauthenticated writes get a strict per-IP budget
		// (5 req/min) so bulk fake-order spam / LLM-cost abuse is impractical.
		// OptionalAuth: a valid Bearer access token upgrades the chat to an
		// authenticated caller — create_booking then attributes the order to
		// the account (no one-order guest limit). No token => pure guest,
		// unchanged. The endpoint stays public; invalid tokens are ignored.
		api.POST("/chat", middlewares.PublicWriteRateLimit(), middlewares.RequestBodyLimit(64<<10), middlewares.OptionalAuth(s.JWT), h.GuestChat)
		api.GET("/chat/history", h.GuestHistory)
		// Public manual order entry for the temporary AI-driven flow:
		// Customer -> AI chat -> select package -> confirm -> order saved as pending.
		// This endpoint never creates DOKU payment/session while PAYMENTS_ENABLED=false.
		api.POST("/orders", middlewares.PublicWriteRateLimit(), middlewares.RequestBodyLimit(64<<10), h.GuestCreateOrder)
		api.GET("/orders/:id", h.GuestGetOrder)

		authGroup := api.Group("/auth")
		authGroup.Use(middlewares.AuthRateLimit())
		{
			authGroup.POST("/register", h.Register)
			authGroup.POST("/login", h.Login)
			authGroup.POST("/refresh", h.Refresh)
			authGroup.POST("/logout", h.Logout)
			authGroup.GET("/me", middlewares.Auth(s.JWT), h.Me)
			// Google OAuth (full-page navigations; redirect-based, not JSON).
			authGroup.GET("/google", h.GoogleLogin)
			authGroup.GET("/google/callback", h.GoogleCallback)
			// Explicit "Link Google Account" — requires an authenticated Vero
			// session so the verified Google identity is attached to the PROVEN
			// account owner (no email-based auto-merge → no account takeover).
			// The link flow returns to its own callback path (same handler; the
			// state's link_user_id selects the linking branch).
			authGroup.GET("/google/link", middlewares.Auth(s.JWT), h.GoogleLinkStart)
			authGroup.GET("/google/link/callback", h.GoogleCallback)
		}

		// SEC-18: SSE broadcasts internal workflow/payment events to every
		// subscriber. Restrict to staff so raw prompts, session IDs, and payment
		// data are not exposed to regular users. Payloads are also sanitized at
		// the publish site as defense-in-depth.
		api.GET("/events/stream", middlewares.Auth(s.JWT), middlewares.Role(models.RoleOperator, models.RoleAdmin), h.EventStream)

		protected := api.Group("")
		protected.Use(middlewares.Auth(s.JWT))
		{
			protected.GET("/chat/sessions", h.ChatSessions)
			protected.GET("/chat/:id/messages", h.ChatMessages)

			protected.GET("/trips", h.ListTrips)
			protected.GET("/trips/:id", h.GetTrip)
			protected.POST("/trips", middlewares.Role(models.RoleOperator, models.RoleAdmin), h.CreateTrip)
			protected.PUT("/trips/:id", middlewares.Role(models.RoleOperator, models.RoleAdmin), h.UpdateTrip)

			admin := protected.Group("/admin")
			admin.Use(middlewares.Role(models.RoleOperator, models.RoleAdmin))
			{
				admin.GET("/packages", h.ListTrips)
				admin.POST("/packages", h.CreateTrip)
				admin.PUT("/packages/:id", h.UpdateTrip)
				admin.DELETE("/packages/:id", h.DeleteTrip)
				admin.POST("/uploads", h.UploadTripMedia)
				admin.GET("/dashboard", h.Analytics)
				// Staff provisioning is admin-only (SEC-1).
				admin.POST("/users", middlewares.Role(models.RoleAdmin), h.AdminCreateUser)
			}

			protected.DELETE("/trips/:id", middlewares.Role(models.RoleOperator, models.RoleAdmin), h.DeleteTrip)

			protected.POST("/bookings", h.CreateBooking)
			// Explicit, idempotent retry for the guest-order claim (GO-P1-3).
			// The claim hooks in Register/Login/GoogleCallback are best-effort
			// and can be skipped silently (guest cookie not sent on the
			// cross-site Google callback, transient DB failure), which strands
			// the order on a guest identity nobody can log into. This endpoint
			// re-runs the SAME transition with the SAME proofs: Bearer token
			// (this middleware group) for the account + vero_guest_session
			// cookie for the guest order. No order id is accepted from the
			// client, so it cannot be pointed at somebody else's order.
			protected.POST("/orders/claim", h.ClaimOrderToAccount)
			protected.GET("/bookings", middlewares.Role(models.RoleOperator, models.RoleAdmin), h.ListBookings)
			protected.GET("/bookings/:id", h.GetBooking)
			protected.PUT("/bookings/:id", middlewares.Role(models.RoleOperator, models.RoleAdmin), h.UpdateBooking)

			// DOKU/payment routes are isolated behind PAYMENTS_ENABLED. Disabled mode
			// preserves handlers/services for future use but prevents payment sessions
			// or DOKU webhook processing during the temporary manual-admin order flow.
			if s.Config.PaymentsEnabled {
				protected.POST("/payments/create", h.CreatePayment)
				protected.GET("/payments/:id", h.GetPayment)
			} else {
				protected.POST("/payments/create", h.PaymentFeatureDisabled)
				protected.GET("/payments/:id", h.PaymentFeatureDisabled)
			}

			protected.GET("/logs", middlewares.Role(models.RoleOperator, models.RoleAdmin), h.Logs)
			protected.GET("/logs/workflows", middlewares.Role(models.RoleOperator, models.RoleAdmin), h.WorkflowLogs)
			protected.GET("/logs/tool-calls", middlewares.Role(models.RoleOperator, models.RoleAdmin), h.ToolCalls)
			protected.GET("/analytics/dashboard", middlewares.Role(models.RoleOperator, models.RoleAdmin), h.Analytics)
		}

		if s.Config.PaymentsEnabled {
			api.POST("/payments/webhook", h.PaymentWebhook)
		} else {
			api.POST("/payments/webhook", h.PaymentFeatureDisabled)
		}
	}
}
