package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// WebhookConfigRepository persists webhook endpoint configuration.
// The signing secret stays in webhook_configs.secret as this branch stores it.
type WebhookConfigRepository struct {
	db.Base
}

// NewWebhookConfigRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWebhookConfigRepository(query orm.Query) *WebhookConfigRepository {
	return &WebhookConfigRepository{Base: db.NewBase(query)}
}

// Create inserts a webhook config, including the secret column as given.
func (r *WebhookConfigRepository) Create(ctx context.Context, cfg *models.WebhookConfig) error {
	if cfg == nil {
		return fmt.Errorf("create webhook config: config is nil")
	}
	if err := r.Query(ctx).Create(cfg); err != nil {
		return fmt.Errorf("create webhook config: %w", err)
	}
	return nil
}

// FindByWalletID returns the configs scoped to a wallet.
func (r *WebhookConfigRepository) FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := r.Query(ctx).Where("wallet_id = ?", walletID).Find(&cfgs); err != nil {
		return nil, fmt.Errorf("list webhook configs: %w", err)
	}
	return cfgs, nil
}

// FindByIDAndWallet returns the config when it belongs to the wallet, or ErrRepositoryNotFound.
func (r *WebhookConfigRepository) FindByIDAndWallet(ctx context.Context, id, walletID uuid.UUID) (*models.WebhookConfig, error) {
	var cfg models.WebhookConfig
	if err := r.Query(ctx).Where("id = ? AND wallet_id = ?", id, walletID).First(&cfg); err != nil {
		return nil, fmt.Errorf("find webhook config: %w", err)
	}
	if cfg.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &cfg, nil
}

// FindActive returns every active config.
func (r *WebhookConfigRepository) FindActive(ctx context.Context) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := r.Query(ctx).Where("is_active", true).Find(&cfgs); err != nil {
		return nil, fmt.Errorf("list active webhook configs: %w", err)
	}
	return cfgs, nil
}

// FindAll returns every config ordered by created_at.
func (r *WebhookConfigRepository) FindAll(ctx context.Context) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := r.Query(ctx).Order("created_at").Find(&cfgs); err != nil {
		return nil, fmt.Errorf("list webhook configs: %w", err)
	}
	return cfgs, nil
}

// FindByID returns the config, or ErrRepositoryNotFound.
func (r *WebhookConfigRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.WebhookConfig, error) {
	var cfg models.WebhookConfig
	if err := r.Query(ctx).Where("id = ?", id).First(&cfg); err != nil {
		return nil, fmt.Errorf("find webhook config: %w", err)
	}
	if cfg.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &cfg, nil
}

// FindVisibleToAccount returns the account's own configs plus legacy configs
// that were created before ownership was recorded.
func (r *WebhookConfigRepository) FindVisibleToAccount(ctx context.Context, accountID uuid.UUID) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := r.Query(ctx).
		Where("wallet_id IS NULL AND (account_id = ? OR account_id IS NULL)", accountID).
		Order("created_at").
		Find(&cfgs); err != nil {
		return nil, fmt.Errorf("list visible webhook configs: %w", err)
	}
	return cfgs, nil
}

// AssignAccount sets account_id and, when the pointer is non-nil, events and is_active.
// It does not read or write the signing secret.
func (r *WebhookConfigRepository) AssignAccount(ctx context.Context, id, accountID uuid.UUID, events *string, isActive *bool) error {
	if id == uuid.Nil {
		return fmt.Errorf("webhook config id is required")
	}
	fields := map[string]any{"account_id": accountID}
	if events != nil {
		fields["events"] = *events
	}
	if isActive != nil {
		fields["is_active"] = *isActive
	}
	if _, err := r.Query(ctx).Model(&models.WebhookConfig{}).Where("id = ?", id).Update(fields); err != nil {
		return fmt.Errorf("assign webhook config: %w", err)
	}
	return nil
}

// Delete removes the config row.
func (r *WebhookConfigRepository) Delete(ctx context.Context, cfg *models.WebhookConfig) error {
	if cfg == nil {
		return fmt.Errorf("delete webhook config: config is nil")
	}
	if _, err := r.Query(ctx).Delete(cfg); err != nil {
		return fmt.Errorf("delete webhook config: %w", err)
	}
	return nil
}

// DeleteByID removes the config with this id.
func (r *WebhookConfigRepository) DeleteByID(ctx context.Context, id uuid.UUID) error {
	if _, err := r.Query(ctx).Where("id = ?", id).Delete(&models.WebhookConfig{}); err != nil {
		return fmt.Errorf("delete webhook config: %w", err)
	}
	return nil
}
