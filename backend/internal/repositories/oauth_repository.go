package repositories

import (
	"context"
	"time"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// OAuth state + Google account-link persistence (Google OAuth, 18 Agu 2026).

func (r *Repository) CreateOAuthState(ctx context.Context, state *models.OAuthState) error {
	return r.DB.WithContext(ctx).Create(state).Error
}

// ConsumeOAuthState atomically marks a state row as consumed in a single
// UPDATE, returning the row only when THIS caller won the consume race
// (rowsAffected == 1). A lost race (0 rows) means the state is unknown,
// already consumed, or expired — the caller must reject the callback. This is
// the same single-winner pattern as AuthSession RotateSession (BUG-1) and is
// what makes the OAuth `state` parameter non-replayable.
func (r *Repository) ConsumeOAuthState(ctx context.Context, stateHash string) (models.OAuthState, bool, error) {
	now := time.Now()
	result := r.DB.WithContext(ctx).Model(&models.OAuthState{}).
		Where("state_hash = ? AND consumed_at IS NULL AND expires_at > ?", stateHash, now).
		Update("consumed_at", now)
	if result.Error != nil {
		return models.OAuthState{}, false, result.Error
	}
	if result.RowsAffected != 1 {
		return models.OAuthState{}, false, nil
	}
	var state models.OAuthState
	if err := r.DB.WithContext(ctx).Where("state_hash = ?", stateHash).First(&state).Error; err != nil {
		return models.OAuthState{}, false, err
	}
	return state, true, nil
}

// DeleteExpiredOAuthStates removes states past expiry (housekeeping; states
// are also rejected at consume time, so this only bounds table growth).
func (r *Repository) DeleteExpiredOAuthStates(ctx context.Context, before time.Time) (int64, error) {
	result := r.DB.WithContext(ctx).
		Where("expires_at < ?", before).
		Delete(&models.OAuthState{})
	return result.RowsAffected, result.Error
}

// FindUserByGoogleSub resolves an account previously linked to a Google `sub`.
func (r *Repository) FindUserByGoogleSub(ctx context.Context, sub string) (models.User, error) {
	var user models.User
	err := r.DB.WithContext(ctx).Where("google_sub = ?", sub).First(&user).Error
	return user, err
}

// LinkUserGoogleSub attaches a Google `sub` to an existing user (account
// linking by verified email). Single-column update — never a full Save (DB-2).
func (r *Repository) LinkUserGoogleSub(ctx context.Context, userID string, sub string) error {
	return r.DB.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", userID).
		Update("google_sub", sub).Error
}
