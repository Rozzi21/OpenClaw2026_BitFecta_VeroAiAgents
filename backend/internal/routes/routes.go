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
		api.POST("/chat", middlewares.PublicWriteRateLimit(), middlewares.RequestBodyLimit(64<<10), h.GuestChat)
		api.GET("/chat/history", h.GuestHistory)
		// Public manual order entry for the temporary AI-driven flow:
		// Customer -> AI chat -> select package -> confirm -> order saved as pending.
		// This endpoint never creates DOKU payment/session while PAYMENTS_ENABLED=false.
		api.POST("/orders", middlewares.PublicWriteRateLimit(), middlewares.RequestBodyLimit(64<<10), h.GuestCreateOrder)

		authGroup := api.Group("/auth")
		authGroup.Use(middlewares.AuthRateLimit())
		{
			authGroup.POST("/register", h.Register)
			authGroup.POST("/login", h.Login)
			authGroup.POST("/refresh", h.Refresh)
			authGroup.POST("/logout", h.Logout)
			authGroup.GET("/me", middlewares.Auth(s.JWT), h.Me)
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
