package repositories

import (
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

func (r *Repository) CreateAILog(log *models.AILog) error {
	return r.DB.Create(log).Error
}

func (r *Repository) ListAILogs(query RepositoryFilter) ([]models.AILog, error) {
	var logs []models.AILog
	err := r.DB.Order("created_at desc").Limit(query.Limit).Offset(query.Offset).Find(&logs).Error
	return logs, err
}

func (r *Repository) CreateToolCall(call *models.ToolCall) error {
	return r.DB.Create(call).Error
}

func (r *Repository) ListToolCalls(query RepositoryFilter) ([]models.ToolCall, error) {
	var calls []models.ToolCall
	err := r.DB.Order("created_at desc").Limit(query.Limit).Offset(query.Offset).Find(&calls).Error
	return calls, err
}
