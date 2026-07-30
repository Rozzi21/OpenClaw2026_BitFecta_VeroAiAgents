package handlers

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

func (h *Handler) Health(c *gin.Context) {
	utils.Success(c, http.StatusOK, "Service healthy", gin.H{
		"service": "vero-travel-api",
		"status":  "healthy",
		"uptime":  time.Since(h.StartedAt).String(),
	})
}

func (h *Handler) DatabaseHealth(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	// SEC-15: do not leak raw DB errors (DSN fragments, connection details) on a
	// public endpoint; log server-side instead.
	if err := h.Database.Health(ctx); err != nil {
		log.Printf("[health] database check failed: %v", err)
		utils.Error(c, http.StatusServiceUnavailable, "Database disconnected", gin.H{})
		return
	}
	utils.Success(c, http.StatusOK, "Database connected", gin.H{"database": "connected"})
}

func (h *Handler) Liveness(c *gin.Context) {
	utils.Success(c, http.StatusOK, "Liveness OK", gin.H{"status": "UP"})
}

func (h *Handler) Readiness(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := h.Database.Health(ctx); err != nil {
		log.Printf("[health] readiness database check failed: %v", err)
		utils.Error(c, http.StatusServiceUnavailable, "Database disconnected", gin.H{"status": "DOWN"})
		return
	}
	utils.Success(c, http.StatusOK, "Readiness OK", gin.H{"status": "UP"})
}
