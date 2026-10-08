package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// RefreshTokenRepository persists refresh tokens.
type RefreshTokenRepository struct {
	db.Base
}

// NewRefreshTokenRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewRefreshTokenRepository(query orm.Query) *RefreshTokenRepository {
	return &RefreshTokenRepository{Base: db.NewBase(query)}
}

// Create inserts a refresh token.
func (r *RefreshTokenRepository) Create(ctx context.Context, token *models.RefreshToken) error {
	if token == nil {
		return fmt.Errorf("create refresh token: token is nil")
	}
	if err := r.Query(ctx).Create(token); err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return nil
}

// FindValidTokens returns refresh tokens that have not expired and are not revoked.
func (r *RefreshTokenRepository) FindValidTokens(ctx context.Context) ([]models.RefreshToken, error) {
	var tokens []models.RefreshToken
	if err := r.Query(ctx).Where("expires_at > ? AND revoked_at IS NULL", time.Now()).Find(&tokens); err != nil {
		return nil, fmt.Errorf("list valid refresh tokens: %w", err)
	}
	return tokens, nil
}

// RevokeByID sets revoked_at on one refresh token.
func (r *RefreshTokenRepository) RevokeByID(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	if _, err := r.Query(ctx).Model(&models.RefreshToken{}).Where("id = ?", id).Update("revoked_at", now); err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

// RevokeIfActive revokes a live refresh token and reports whether this call
// did it. A rotation that finds the token already revoked must not mint a new pair.
func (r *RefreshTokenRepository) RevokeIfActive(ctx context.Context, id uuid.UUID) (bool, error) {
	result, err := r.Query(ctx).Model(&models.RefreshToken{}).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", time.Now())
	if err != nil {
		return false, fmt.Errorf("revoke refresh token: %w", err)
	}
	return result.RowsAffected == 1, nil
}

// RevokeAllForUser sets revoked_at on the user's live refresh tokens.
func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	now := time.Now()
	if _, err := r.Query(ctx).Model(&models.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now); err != nil {
		return fmt.Errorf("revoke user refresh tokens: %w", err)
	}
	return nil
}
