package repositories

import (
	"context"
	"fmt"
	"strings"

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
	if strings.TrimSpace(token.SpendingLimit) == "" {
		token.SpendingLimit = "{}"
	}
	query := r.Query(ctx)
	if strings.TrimSpace(token.Permissions) == "" {
		// NULL is the omitted grant. An empty string is not valid jsonb.
		query = query.Omit("Permissions")
	}
	if err := query.Create(token); err != nil {
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

// DeleteByAccountAndCreator removes every token this user created for the account.
// Tokens with a null creator, and tokens on other accounts, stay.
func (r *AccessTokenRepository) DeleteByAccountAndCreator(ctx context.Context, accountID, createdBy uuid.UUID) error {
	if accountID == uuid.Nil || createdBy == uuid.Nil {
		return fmt.Errorf("delete access tokens: account id and creator are required")
	}
	if _, err := r.Query(ctx).Where("account_id = ? AND created_by = ?", accountID, createdBy).Delete(&models.AccessToken{}); err != nil {
		return fmt.Errorf("delete access tokens: %w", err)
	}
	return nil
}

// RecordUse stamps last_used_at. A revoked row is left alone. updated_at
// stays as stored so the stamp is not a second write the caller can see.
func (r *AccessTokenRepository) RecordUse(ctx context.Context, tokenID, accountID uuid.UUID) error {
	if tokenID == uuid.Nil || accountID == uuid.Nil {
		return fmt.Errorf("record access token use: token id and account id are required")
	}
	_, err := r.Query(ctx).Exec(
		`UPDATE access_tokens SET last_used_at = NOW() WHERE id = ? AND account_id = ? AND revoked_at IS NULL`,
		tokenID, accountID,
	)
	if err != nil {
		return fmt.Errorf("record access token use: %w", err)
	}
	return nil
}

// MarkRevoked sets revoked_at when it is still empty. The row stays.
// A second call keeps the original stamp and reports that nothing changed.
func (r *AccessTokenRepository) MarkRevoked(ctx context.Context, tokenID, accountID uuid.UUID) (bool, error) {
	if tokenID == uuid.Nil || accountID == uuid.Nil {
		return false, fmt.Errorf("revoke access token: token id and account id are required")
	}
	result, err := r.Query(ctx).Exec(
		`UPDATE access_tokens SET revoked_at = NOW() WHERE id = ? AND account_id = ? AND revoked_at IS NULL`,
		tokenID, accountID,
	)
	if err != nil {
		return false, fmt.Errorf("revoke access token: %w", err)
	}
	if result == nil {
		return false, fmt.Errorf("revoke access token: no result")
	}
	return result.RowsAffected > 0, nil
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
