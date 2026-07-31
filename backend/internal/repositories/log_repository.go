package repositories

import (
	"context"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

func (r *Repository) CreateAILog(ctx context.Context, log *models.AILog) error {
	return r.DB.WithContext(ctx).Create(log).Error
}

func (r *Repository) ListAILogs(ctx context.Context, query RepositoryFilter) ([]models.AILog, error) {
	var logs []models.AILog
	err := r.DB.WithContext(ctx).Order("created_at desc").Limit(query.Limit).Offset(query.Offset).Find(&logs).Error
	return logs, err
}

func (r *Repository) CreateToolCall(ctx context.Context, call *models.ToolCall) error {
	return r.DB.WithContext(ctx).Create(call).Error
}

func (r *Repository) ListToolCalls(ctx context.Context, query RepositoryFilter) ([]models.ToolCall, error) {
	var calls []models.ToolCall
	err := r.DB.WithContext(ctx).Order("created_at desc").Limit(query.Limit).Offset(query.Offset).Find(&calls).Error
	return calls, err
}
