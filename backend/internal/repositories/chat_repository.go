package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

func (r *Repository) CreateChatSession(ctx context.Context, session *models.ChatSession) error {
	return r.DB.WithContext(ctx).Create(session).Error
}

func (r *Repository) FindChatSession(ctx context.Context, id uuid.UUID) (models.ChatSession, error) {
	var session models.ChatSession
	err := r.DB.WithContext(ctx).First(&session, "id = ?", id).Error
	return session, err
}

func (r *Repository) UpdateChatSession(ctx context.Context, session *models.ChatSession) error {
	return r.DB.WithContext(ctx).Save(session).Error
}

func (r *Repository) UpdateChatSessionMemorySummary(ctx context.Context, sessionID uuid.UUID, summary string) error {
	return r.DB.WithContext(ctx).Model(&models.ChatSession{}).Where("id = ?", sessionID).Update("memory_summary", summary).Error
}

func (r *Repository) UpdateChatSessionSelectedTrip(ctx context.Context, sessionID uuid.UUID, tripID *uuid.UUID) error {
	return r.DB.WithContext(ctx).Model(&models.ChatSession{}).Where("id = ?", sessionID).Update("selected_trip_id", tripID).Error
}

func (r *Repository) UpdateChatSessionActivity(ctx context.Context, sessionID uuid.UUID, expiresAt, lastActivityAt time.Time) error {
	return r.DB.WithContext(ctx).Model(&models.ChatSession{}).Where("id = ?", sessionID).Updates(map[string]interface{}{
		"expires_at":       expiresAt,
		"last_activity_at": lastActivityAt,
	}).Error
}

func (r *Repository) ListChatSessions(ctx context.Context, userID uuid.UUID) ([]models.ChatSession, error) {
	var sessions []models.ChatSession
	err := r.DB.WithContext(ctx).Where("user_id = ?", userID).Order("created_at desc").Find(&sessions).Error
	return sessions, err
}

func (r *Repository) DeleteExpiredChatSessions(ctx context.Context, before time.Time) (int64, error) {
	// SEC-19: Must delete child records (chat_messages, tool_calls, ai_logs)
	// before deleting the session, otherwise they become orphans in DB.
	tx := r.DB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}

	var sessions []models.ChatSession
	if err := tx.Select("id").Where("expires_at IS NOT NULL AND expires_at < ?", before).Find(&sessions).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if len(sessions) == 0 {
		tx.Rollback()
		return 0, nil
	}

	var ids []string
	for _, s := range sessions {
		ids = append(ids, s.ID.String())
	}

	// Unscoped() is used to hard delete the orphans, since keeping soft deleted
	// records for anonymous guest sessions just wastes space.
	if err := tx.Unscoped().Where("session_id IN ?", ids).Delete(&models.ChatMessage{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Unscoped().Where("session_id IN ?", ids).Delete(&models.ToolCall{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Unscoped().Where("session_id IN ?", ids).Delete(&models.AILog{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	result := tx.Where("id IN ?", ids).Delete(&models.ChatSession{})
	if result.Error != nil {
		tx.Rollback()
		return 0, result.Error
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

func (r *Repository) CountExpiredChatSessions(ctx context.Context, before time.Time) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&models.ChatSession{}).Where("expires_at IS NOT NULL AND expires_at < ?", before).Count(&count).Error
	return count, err
}

func (r *Repository) AddChatMessage(ctx context.Context, message *models.ChatMessage) error {
	return r.DB.WithContext(ctx).Create(message).Error
}

func (r *Repository) ListChatMessages(ctx context.Context, sessionID uuid.UUID) ([]models.ChatMessage, error) {
	var messages []models.ChatMessage
	err := r.DB.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at asc").Find(&messages).Error
	return messages, err
}

func (r *Repository) ListRecentChatMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]models.ChatMessage, error) {
	var newest []models.ChatMessage
	if limit <= 0 {
		limit = 8
	}
	if err := r.DB.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at desc").Limit(limit).Find(&newest).Error; err != nil {
		return nil, err
	}
	messages := make([]models.ChatMessage, len(newest))
	for i := range newest {
		messages[len(newest)-1-i] = newest[i]
	}
	return messages, nil
}

func (r *Repository) CountChatMessages(ctx context.Context, sessionID uuid.UUID) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&models.ChatMessage{}).Where("session_id = ?", sessionID).Count(&count).Error
	return count, err
}

// TailChatMessages returns the last N messages (oldest-first) for a chat session.
// Unlike ListChatMessages which loads ALL messages, this only fetches the tail,
// making it efficient for memory-summary refresh on long conversations.
func (r *Repository) TailChatMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]models.ChatMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	var newest []models.ChatMessage
	if err := r.DB.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at desc").Limit(limit).Find(&newest).Error; err != nil {
		return nil, err
	}
	messages := make([]models.ChatMessage, len(newest))
	for i := range newest {
		messages[len(newest)-1-i] = newest[i]
	}
	return messages, nil
}
