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

// PasswordResetTokenRepository persists password reset tokens.
type PasswordResetTokenRepository struct {
	db.Base
}

// NewPasswordResetTokenRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewPasswordResetTokenRepository(query orm.Query) *PasswordResetTokenRepository {
	return &PasswordResetTokenRepository{Base: db.NewBase(query)}
}

// Create inserts a password reset token.
func (r *PasswordResetTokenRepository) Create(ctx context.Context, token *models.PasswordResetToken) error {
	if token == nil {
		return fmt.Errorf("create password reset token: token is nil")
	}
	if err := r.Query(ctx).Create(token); err != nil {
		return fmt.Errorf("create password reset token: %w", err)
	}
	return nil
}

// FindValidTokens returns reset tokens that have not expired and have not been used.
func (r *PasswordResetTokenRepository) FindValidTokens(ctx context.Context) ([]models.PasswordResetToken, error) {
	var tokens []models.PasswordResetToken
	if err := r.Query(ctx).Where("expires_at > ? AND used_at IS NULL", time.Now()).Find(&tokens); err != nil {
		return nil, fmt.Errorf("list valid password reset tokens: %w", err)
	}
	return tokens, nil
}

// MarkUsed sets used_at on a password reset token.
func (r *PasswordResetTokenRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	if _, err := r.Query(ctx).Model(&models.PasswordResetToken{}).Where("id = ?", id).Update("used_at", now); err != nil {
		return fmt.Errorf("mark password reset token used: %w", err)
	}
	return nil
}
