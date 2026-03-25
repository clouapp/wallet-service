package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type UserRepository interface {
	FindByEmail(email string) (*models.User, error)
	FindByID(id uuid.UUID) (*models.User, error)
	Create(user *models.User) error
	UpdateFullName(id uuid.UUID, fullName string) error
	UpdatePasswordHash(id uuid.UUID, hash string) error
}

type userRepository struct{}

func NewUserRepository() UserRepository {
	return &userRepository{}
}

func (r *userRepository) FindByEmail(email string) (*models.User, error) {
	var user models.User
	err := facades.Orm().Query().Where("email = ?", email).First(&user)
	if err != nil {
		return nil, err
	}
	if user.ID == uuid.Nil {
		return nil, nil
	}
	return &user, nil
}

func (r *userRepository) FindByID(id uuid.UUID) (*models.User, error) {
	var user models.User
	err := facades.Orm().Query().Where("id = ?", id).First(&user)
	if err != nil {
		return nil, err
	}
	if user.ID == uuid.Nil {
		return nil, nil
	}
	return &user, nil
}

func (r *userRepository) Create(user *models.User) error {
	return facades.Orm().Query().Create(user)
}

func (r *userRepository) UpdateFullName(id uuid.UUID, fullName string) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("full_name", fullName)
	return err
}

func (r *userRepository) UpdatePasswordHash(id uuid.UUID, hash string) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("password_hash", hash)
	return err
}
