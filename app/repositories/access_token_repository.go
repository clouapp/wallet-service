package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// AccessTokenRepository persists API access tokens.
type AccessTokenRepository struct {
	db.Base
}

// NewAccessTokenRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewAccessTokenRepository(query orm.Query) *AccessTokenRepository {
	return &AccessTokenRepository{Base: db.NewBase(query)}
}

// Create inserts an access token.
func (r *AccessTokenRepository) Create(ctx context.Context, token *models.AccessToken) error {
	if token == nil {
		return fmt.Errorf("create access token: token is nil")
	}
	if err := r.Query(ctx).Create(token); err != nil {
		return fmt.Errorf("create access token: %w", err)
	}
	return nil
}

// FindByAccountID returns the account's access tokens.
func (r *AccessTokenRepository) FindByAccountID(ctx context.Context, accountID uuid.UUID) ([]models.AccessToken, error) {
	var tokens []models.AccessToken
	if err := r.Query(ctx).Where("account_id = ?", accountID).Find(&tokens); err != nil {
		return nil, fmt.Errorf("list access tokens: %w", err)
	}
	return tokens, nil
}

// PaginateByAccountID pages the account's access tokens.
func (r *AccessTokenRepository) PaginateByAccountID(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccessToken, int64, error) {
	total, err := r.Query(ctx).Model(&models.AccessToken{}).Where("account_id = ?", accountID).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count access tokens: %w", err)
	}
	var tokens []models.AccessToken
	if err := r.Query(ctx).Where("account_id = ?", accountID).Offset(offset).Limit(limit).Find(&tokens); err != nil {
		return nil, 0, fmt.Errorf("list access tokens: %w", err)
	}
	return tokens, total, nil
}

// FindByIDAndAccount returns the token when it belongs to the account, or ErrRepositoryNotFound.
func (r *AccessTokenRepository) FindByIDAndAccount(ctx context.Context, tokenID, accountID uuid.UUID) (*models.AccessToken, error) {
	var token models.AccessToken
	if err := r.Query(ctx).Where("id = ? AND account_id = ?", tokenID, accountID).First(&token); err != nil {
		return nil, fmt.Errorf("find access token: %w", err)
	}
	if token.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &token, nil
}

// Delete removes an access token.
func (r *AccessTokenRepository) Delete(ctx context.Context, token *models.AccessToken) error {
	if token == nil {
		return fmt.Errorf("delete access token: token is nil")
	}
	if _, err := r.Query(ctx).Delete(token); err != nil {
		return fmt.Errorf("delete access token: %w", err)
	}
	return nil
}
