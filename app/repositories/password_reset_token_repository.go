package repositories

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type PasswordResetTokenRepository interface {
	Create(token *models.PasswordResetToken) error
	FindValidTokens() ([]models.PasswordResetToken, error)
	MarkUsed(id uuid.UUID) error
}

type passwordResetTokenRepository struct{}

func NewPasswordResetTokenRepository() PasswordResetTokenRepository {
	return &passwordResetTokenRepository{}
}

func (r *passwordResetTokenRepository) Create(token *models.PasswordResetToken) error {
	return facades.Orm().Query().Create(token)
}

func (r *passwordResetTokenRepository) FindValidTokens() ([]models.PasswordResetToken, error) {
	var tokens []models.PasswordResetToken
	err := facades.Orm().Query().
		Where("expires_at > ? AND used_at IS NULL", time.Now()).
		Find(&tokens)
	return tokens, err
}

func (r *passwordResetTokenRepository) MarkUsed(id uuid.UUID) error {
	now := time.Now()
	_, err := facades.Orm().Query().
		Model(&models.PasswordResetToken{}).
		Where("id = ?", id).
		Update("used_at", now)
	return err
}
