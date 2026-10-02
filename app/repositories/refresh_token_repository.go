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
	RevokeIfActive(id uuid.UUID) (bool, error)
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

// RevokeIfActive revokes a refresh token and reports whether this call did
// it. A rotation that finds the token already revoked lost a race (or the
// session was revoked meanwhile) and must not mint a new pair.
func (r *refreshTokenRepository) RevokeIfActive(id uuid.UUID) (bool, error) {
	result, err := facades.Orm().Query().
		Model(&models.RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", time.Now())
	if err != nil {
		return false, err
	}
	return result.RowsAffected == 1, nil
}

func (r *refreshTokenRepository) RevokeAllForUser(userID uuid.UUID) error {
	now := time.Now()
	_, err := facades.Orm().Query().
		Model(&models.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now)
	return err
}
