package main

import (
	"context"
	"log/slog"

	"errors"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/database"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/events"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/handlers"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/middlewares"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/routes"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// Initialize structured logging with slog and inject context handler (PRR-P2-1)
	var logHandler slog.Handler
	if cfg.AppEnv == "production" {
		logHandler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
		gin.SetMode(gin.ReleaseMode)
	} else {
		logHandler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(utils.NewContextHandler(logHandler)))

	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("failed to close database: %v", err)
		}
	}()

	if err := db.AutoMigrate(); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	repo := repositories.New(db.DB)
	bus := events.NewBus()
	jwtService := auth.NewJWTService(cfg)
	serviceContainer := services.New(cfg, repo, jwtService, bus)
	handler := handlers.New(serviceContainer, db)

	router := gin.New()
	// Limit multipart memory buffering for uploads (SEC-5).
	router.MaxMultipartMemory = 8 << 20 // 8 MiB
	// SEC-14: in dev, trust no proxy so X-Forwarded-For cannot be spoofed. In
	// production, trust only the configured reverse proxy CIDR(s).
	if cfg.AppEnv == "production" {
		if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
			log.Fatalf("invalid TRUSTED_PROXIES: %v", err)
		}
	} else {
		router.SetTrustedProxies(nil)
	}
	router.Use(
		middlewares.RequestID(),
		middlewares.SecureHeaders(),
		middlewares.CORS(cfg.CORSAllowedOrigins),
		middlewares.RateLimit(),
		middlewares.Metrics(),          // Record Prometheus metrics (PRR-P1-1)
		middlewares.StructuredLogger(), // Structured request logs with slog (PRR-P2-1)
		middlewares.Recovery(),
	)
	router.Static("/uploads", "./uploads")
	routes.Register(router, handler, serviceContainer)
	startChatSessionCleanup(serviceContainer)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second, // Protect against slow-write attacks globally. Extended/disabled dynamically in handlers (e.g. SSE).
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("vero-travel-api listening on :%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("server shutdown failed: %v", err)
	}
	log.Println("server stopped gracefully")
}

// startChatSessionCleanup is the MVP adapter for the cleanup use case. The
// service method is scheduler-agnostic, so a future cron/systemd/Kubernetes
// job can invoke the same operation without moving SQL into the scheduler.
func startChatSessionCleanup(s *services.Services) {
	interval := time.Hour
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			// SEC-26: cleanup runs on a background ticker (no HTTP request), so
			// it uses context.Background(); a per-run timeout avoids hanging on
			// a wedged DB connection indefinitely.
			runCtx, cancelRun := context.WithTimeout(context.Background(), 30*time.Second)
			deleted, err := s.AI.CleanupExpiredChatSessions(runCtx, time.Now())
			cancelRun()
			if err != nil {
				log.Printf("[chat-session-cleanup] failed: %v", err)
				continue
			}
			if deleted > 0 {
				log.Printf("[chat-session-cleanup] deleted=%d", deleted)
			}
		}
	}()
}
