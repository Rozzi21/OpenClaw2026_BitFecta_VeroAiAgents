package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"gorm.io/gorm"
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

// FindUserByGoogleSub resolves the Vero account linked to a Google `sub` via
// the canonical external_identities mapping (identity keyed by sub, NOT email).
// users.google_sub is only a denormalized fast-path mirror; the source of truth
// is the ExternalIdentity row (UNIQUE(provider, provider_user_id)).
func (r *Repository) FindUserByGoogleSub(ctx context.Context, sub string) (models.User, error) {
	var ident models.ExternalIdentity
	err := r.DB.WithContext(ctx).
		Where("provider = ? AND provider_user_id = ?", models.ExternalIdentityProviderGoogle, sub).
		First(&ident).Error
	if err != nil {
		return models.User{}, err
	}
	var user models.User
	if err := r.DB.WithContext(ctx).Where("id = ?", ident.UserID).First(&user).Error; err != nil {
		return models.User{}, err
	}
	return user, nil
}

// CreateUserWithGoogleIdentity creates a brand-new Vero user AND its canonical
// Google ExternalIdentity row in one transaction, mirroring users.google_sub.
// Used when no existing account matches (first-time Google signup).
func (r *Repository) CreateUserWithGoogleIdentity(ctx context.Context, user *models.User, sub string, email string, picture string) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		ident := models.ExternalIdentity{
			UserID:         user.ID,
			Provider:       models.ExternalIdentityProviderGoogle,
			ProviderUserID: sub,
			Email:          email,
			Picture:        picture,
		}
		return tx.Create(&ident).Error
	})
}

// LinkUserGoogleSub attaches a Google `sub` to an existing user by creating the
// canonical ExternalIdentity row (the sub→user mapping) AND mirroring
// users.google_sub for fast reverse lookup. Runs in one transaction so the two
// never diverge. The UNIQUE(provider, provider_user_id) constraint makes the
// link idempotent-safe: a duplicate link attempt surfaces a constraint error.
func (r *Repository) LinkUserGoogleSub(ctx context.Context, userID string, sub string, email string, picture string) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ident := models.ExternalIdentity{
			UserID:         uuid.MustParse(userID),
			Provider:       models.ExternalIdentityProviderGoogle,
			ProviderUserID: sub,
			Email:          email,
			Picture:        picture,
		}
		if err := tx.Create(&ident).Error; err != nil {
			return err
		}
		// Mirror into users.google_sub (single-column update, never Save — DB-2).
		return tx.Model(&models.User{}).
			Where("id = ?", userID).
			Update("google_sub", sub).Error
	})
}
