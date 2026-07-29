package services

import (
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

type LogService struct{ repo *repositories.Repository }

func (s *LogService) Logs(query dto.ListQuery) ([]models.AILog, error) {
	repoQuery := repositories.RepositoryFilter{
		Limit:  query.Limit,
		Offset: query.Offset,
	}
	return s.repo.ListAILogs(repoQuery)
}
func (s *LogService) ToolCalls(query dto.ListQuery) ([]models.ToolCall, error) {
	repoQuery := repositories.RepositoryFilter{
		Limit:  query.Limit,
		Offset: query.Offset,
	}
	return s.repo.ListToolCalls(repoQuery)
}
