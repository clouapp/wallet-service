package repositories

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type RefreshTokenRepository interface {
	Create(token *models.RefreshToken) error
	FindValidTokens() ([]models.RefreshToken, error)
	RevokeByID(id uuid.UUID) error
	RevokeAllForUser(userID uuid.UUID) error
}

type refreshTokenRepository struct{}

func NewRefreshTokenRepository() RefreshTokenRepository {
	return &refreshTokenRepository{}
}

func (r *refreshTokenRepository) Create(token *models.RefreshToken) error {
	return facades.Orm().Query().Create(token)
}

func (r *refreshTokenRepository) FindValidTokens() ([]models.RefreshToken, error) {
	var tokens []models.RefreshToken
	err := facades.Orm().Query().
		Where("expires_at > ? AND revoked_at IS NULL", time.Now()).
		Find(&tokens)
	return tokens, err
}

func (r *refreshTokenRepository) RevokeByID(id uuid.UUID) error {
	now := time.Now()
	_, err := facades.Orm().Query().
		Model(&models.RefreshToken{}).
		Where("id = ?", id).
		Update("revoked_at", now)
	return err
}

func (r *refreshTokenRepository) RevokeAllForUser(userID uuid.UUID) error {
	now := time.Now()
	_, err := facades.Orm().Query().
		Model(&models.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now)
	return err
}
