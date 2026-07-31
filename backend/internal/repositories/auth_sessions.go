package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"gorm.io/gorm"
)

func (r *Repository) CreateAuthSession(ctx context.Context, userID uuid.UUID, tokenJTI string, expiresAt time.Time) error {
	session := models.AuthSession{
		UserID:    userID,
		TokenJTI:  tokenJTI,
		ExpiresAt: expiresAt,
	}
	return r.DB.WithContext(ctx).Create(&session).Error
}

func (r *Repository) FindActiveSessionByJTI(ctx context.Context, tokenJTI string) (models.AuthSession, error) {
	var session models.AuthSession
	err := r.DB.WithContext(ctx).Where(
		"token_jti = ? AND revoked_at IS NULL AND expires_at > ?",
		tokenJTI,
		time.Now(),
	).First(&session).Error
	return session, err
}

func (r *Repository) FindSessionByJTI(ctx context.Context, tokenJTI string) (models.AuthSession, error) {
	var session models.AuthSession
	err := r.DB.WithContext(ctx).Where("token_jti = ?", tokenJTI).First(&session).Error
	return session, err
}

func (r *Repository) RevokeSessionByJTI(ctx context.Context, tokenJTI string) error {
	now := time.Now()
	return r.DB.WithContext(ctx).Model(&models.AuthSession{}).
		Where("token_jti = ? AND revoked_at IS NULL", tokenJTI).
		Update("revoked_at", now).Error
}

// RotateSession atomically revokes an active (non-revoked, non-expired) session
// in a single UPDATE. It reports whether THIS caller won the rotation race
// (rowsAffected == 1). A loser of the race (rowsAffected == 0) is NOT proof of
// token theft: a concurrent legitimate refresh may have rotated the session
// first, so callers must not escalate to revoke-all solely from !rotated.
func (r *Repository) RotateSession(ctx context.Context, tokenJTI string) (rotated bool, err error) {
	now := time.Now()
	result := r.DB.WithContext(ctx).Model(&models.AuthSession{}).
		Where("token_jti = ? AND revoked_at IS NULL AND expires_at > ?", tokenJTI, now).
		Update("revoked_at", now)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// RevokeAllActiveSessionsByUser revokes every active (non-revoked) session for a
// user. Used as a defensive measure when refresh token reuse is detected, which
// is a strong indicator of token theft: invalidating all sessions forces a fresh
// login across every device.
func (r *Repository) RevokeAllActiveSessionsByUser(ctx context.Context, userID uuid.UUID) error {
	return r.DB.WithContext(ctx).Model(&models.AuthSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", time.Now()).Error
}

func (r *Repository) IsSessionRevoked(ctx context.Context, tokenJTI string) (bool, error) {
	var session models.AuthSession
	err := r.DB.WithContext(ctx).Where("token_jti = ?", tokenJTI).First(&session).Error
	if err != nil {
		return false, err
	}
	return session.RevokedAt != nil, nil
}

func (r *Repository) RevokeSessionByJTIIfExists(ctx context.Context, tokenJTI string) error {
	result := r.DB.WithContext(ctx).Model(&models.AuthSession{}).
		Where("token_jti = ? AND revoked_at IS NULL", tokenJTI).
		Update("revoked_at", time.Now())
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func (r *Repository) CountActiveSessionsByJTI(ctx context.Context, tokenJTI string) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&models.AuthSession{}).
		Where("token_jti = ? AND revoked_at IS NULL AND expires_at > ?", tokenJTI, time.Now()).
		Count(&count).Error
	return count, err
}

// EnsureRevokeIsIdempotent allows logout on already-revoked sessions without error.
func (r *Repository) RevokeSessionByJTIAllowMissing(ctx context.Context, tokenJTI string) error {
	err := r.RevokeSessionByJTI(ctx, tokenJTI)
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	return err
}
