package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// TokenRepository persists tokens configured on a chain.
type TokenRepository struct {
	db.Base
}

// NewTokenRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewTokenRepository(query orm.Query) *TokenRepository {
	return &TokenRepository{Base: db.NewBase(query)}
}

// FindByChainID returns the active tokens of a chain.
func (r *TokenRepository) FindByChainID(ctx context.Context, chainID string) ([]models.Token, error) {
	var tokens []models.Token
	if err := r.Query(ctx).Where("chain_id", chainID).Where("status", "active").Find(&tokens); err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	return tokens, nil
}

// FindActive returns every active token.
func (r *TokenRepository) FindActive(ctx context.Context) ([]models.Token, error) {
	var tokens []models.Token
	if err := r.Query(ctx).Where("status", "active").Find(&tokens); err != nil {
		return nil, fmt.Errorf("list active tokens: %w", err)
	}
	return tokens, nil
}

// FindByID returns the token, or ErrRepositoryNotFound.
func (r *TokenRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.Token, error) {
	var token models.Token
	if err := r.Query(ctx).Where("id", id).First(&token); err != nil {
		return nil, fmt.Errorf("find token: %w", err)
	}
	if token.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &token, nil
}

// Create inserts a token.
func (r *TokenRepository) Create(ctx context.Context, token *models.Token) error {
	if token == nil {
		return fmt.Errorf("create token: token is nil")
	}
	if err := r.Query(ctx).Create(token); err != nil {
		return fmt.Errorf("create token: %w", err)
	}
	return nil
}
