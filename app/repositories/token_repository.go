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

// FindAllByChainID returns every token of a chain whatever its status, by symbol.
func (r *TokenRepository) FindAllByChainID(ctx context.Context, chainID string) ([]models.Token, error) {
	var tokens []models.Token
	if err := r.Query(ctx).Where("chain_id", chainID).Order("symbol").Order("contract_address").Find(&tokens); err != nil {
		return nil, fmt.Errorf("list chain tokens: %w", err)
	}
	return tokens, nil
}

// SetStatus writes tokens.status, active or inactive. A missing token is
// ErrRepositoryNotFound.
func (r *TokenRepository) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	if status != models.TokenStatusActive && status != models.TokenStatusInactive {
		return fmt.Errorf("set token status: unknown status %q", status)
	}
	result, err := r.Query(ctx).Model(&models.Token{}).Where("id", id).Update("status", status)
	if err != nil {
		return fmt.Errorf("set token status: %w", err)
	}
	return db.RequireRow(result)
}

// FindByID returns the token, or ErrRepositoryNotFound.
func (r *TokenRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.Token, error) {
	var token models.Token
	if err := r.Query(ctx).Where("id", id).FirstOrFail(&token); err != nil {
		return nil, db.LookupError(err, "find token")
	}
	return &token, nil
}

// FindByChainAndContract returns the token for a chain and contract, or
// ErrRepositoryNotFound. Status is not filtered: a disabled row is still present.
func (r *TokenRepository) FindByChainAndContract(ctx context.Context, chainID, contractAddress string) (*models.Token, error) {
	if chainID == "" || contractAddress == "" {
		return nil, models.ErrRepositoryNotFound
	}
	var token models.Token
	if err := r.Query(ctx).Where("chain_id", chainID).Where("contract_address", contractAddress).FirstOrFail(&token); err != nil {
		return nil, db.LookupError(err, "find token by contract")
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
