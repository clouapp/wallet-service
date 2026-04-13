package repositories

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type UserRepository interface {
	FindByEmail(email string) (*models.User, error)
	FindByID(id uuid.UUID) (*models.User, error)
	Create(user *models.User) error
	UpdateDefaultAccountID(id uuid.UUID, defaultAccountID *uuid.UUID) error
	UpdateFullName(id uuid.UUID, fullName string) error
	UpdatePasswordHash(id uuid.UUID, hash string) error
	UpdatePreferences(id uuid.UUID, prefs *models.UserPreferences) error
	UpdateTotpSecret(id uuid.UUID, secret string) error
	EnableTotp(id uuid.UUID) error
	DisableTotp(id uuid.UUID) error
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

func (r *userRepository) UpdateDefaultAccountID(id uuid.UUID, defaultAccountID *uuid.UUID) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("default_account_id", defaultAccountID)
	return err
}

func (r *userRepository) UpdateFullName(id uuid.UUID, fullName string) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("full_name", fullName)
	return err
}

func (r *userRepository) UpdatePasswordHash(id uuid.UUID, hash string) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("password_hash", hash)
	return err
}

func (r *userRepository) UpdatePreferences(id uuid.UUID, prefs *models.UserPreferences) error {
	jsonBytes, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	_, err = facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("preferences", string(jsonBytes))
	return err
}

func (r *userRepository) UpdateTotpSecret(id uuid.UUID, secret string) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("totp_secret", secret)
	return err
}

func (r *userRepository) EnableTotp(id uuid.UUID) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("totp_enabled", true)
	return err
}

func (r *userRepository) DisableTotp(id uuid.UUID) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update(map[string]interface{}{
		"totp_enabled": false,
		"totp_secret":  "",
	})
	return err
}
