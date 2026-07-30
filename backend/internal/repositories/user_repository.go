package repositories

import (
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

func (r *Repository) CreateUser(user *models.User) error {
	return r.DB.Create(user).Error
}

func (r *Repository) FindUserByEmail(email string) (models.User, error) {
	var user models.User
	err := r.DB.Where("email = ?", email).First(&user).Error
	return user, err
}

func (r *Repository) FirstOrCreateUser(user *models.User) error {
	return r.DB.Where("email = ?", user.Email).FirstOrCreate(user).Error
}

func (r *Repository) FindUserByID(id uuid.UUID) (models.User, error) {
	var user models.User
	err := r.DB.First(&user, "id = ?", id).Error
	return user, err
}
