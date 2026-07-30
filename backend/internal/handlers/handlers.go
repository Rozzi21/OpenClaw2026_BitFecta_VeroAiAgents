package handlers

import (
	"time"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/database"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

type Handler struct {
	Services  *services.Services
	Database  *database.Database
	StartedAt time.Time
}

func New(s *services.Services, db *database.Database) *Handler {
	return &Handler{Services: s, Database: db, StartedAt: time.Now()}
}
