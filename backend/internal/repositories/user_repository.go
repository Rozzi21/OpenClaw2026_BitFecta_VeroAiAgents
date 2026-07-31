package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

func (r *Repository) CreateUser(ctx context.Context, user *models.User) error {
	return r.DB.WithContext(ctx).Create(user).Error
}

func (r *Repository) FindUserByEmail(ctx context.Context, email string) (models.User, error) {
	var user models.User
	err := r.DB.WithContext(ctx).Where("email = ?", email).First(&user).Error
	return user, err
}

func (r *Repository) FirstOrCreateUser(ctx context.Context, user *models.User) error {
	return r.DB.WithContext(ctx).Where("email = ?", user.Email).FirstOrCreate(user).Error
}

func (r *Repository) FindUserByID(ctx context.Context, id uuid.UUID) (models.User, error) {
	var user models.User
	err := r.DB.WithContext(ctx).First(&user, "id = ?", id).Error
	return user, err
}
