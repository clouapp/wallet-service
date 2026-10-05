package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/app/services/settings"
)

const webhookSecretColumn = "secret"

// WebhookConfigRepository persists webhook endpoint configuration.
// The signing secret is sealed at rest with the enc:v1: prefix. Create and
// UpdateFields seal it, and every Find opens it, so callers always see the
// plaintext. An empty secret stays empty. A value without the prefix fails closed.
type WebhookConfigRepository struct {
	db.Base
	cipher settings.Cipher
}

// WebhookConfigRepositoryDeps is the query and the cipher that seals webhook secrets.
// Query may be nil, which starts a fresh query per call. Cipher is required.
type WebhookConfigRepositoryDeps struct {
	Query  orm.Query
	Cipher settings.Cipher
}

// NewWebhookConfigRepository wraps an orm.Query. A nil Query starts a fresh query per call.
// Cipher seals and opens webhook_configs.secret; it is required.
func NewWebhookConfigRepository(deps WebhookConfigRepositoryDeps) *WebhookConfigRepository {
	if deps.Cipher == nil {
		panic("webhook config repository: cipher is required")
	}
	return &WebhookConfigRepository{Base: db.NewBase(deps.Query), cipher: deps.Cipher}
}

// Create inserts a webhook config. The stored secret is sealed; cfg.Secret stays plaintext.
func (r *WebhookConfigRepository) Create(ctx context.Context, cfg *models.WebhookConfig) error {
	if cfg == nil {
		return fmt.Errorf("create webhook config: config is nil")
	}
	plaintext := cfg.Secret
	sealed, err := settings.Seal(r.cipher, plaintext)
	if err != nil {
		return fmt.Errorf("create webhook config: %w", err)
	}
	cfg.Secret = sealed
	createErr := r.Query(ctx).Create(cfg)
	cfg.Secret = plaintext
	if createErr != nil {
		return fmt.Errorf("create webhook config: %w", createErr)
	}
	return nil
}

// FindByWalletID returns the configs scoped to a wallet, with secrets opened.
func (r *WebhookConfigRepository) FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := r.Query(ctx).Where("wallet_id = ?", walletID).Find(&cfgs); err != nil {
		return nil, fmt.Errorf("list webhook configs: %w", err)
	}
	return r.openWebhookSecrets(cfgs)
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
	return r.openWebhookSecret(&cfg)
}

// FindActive returns every active config, with secrets opened.
func (r *WebhookConfigRepository) FindActive(ctx context.Context) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := r.Query(ctx).Where("is_active", true).Find(&cfgs); err != nil {
		return nil, fmt.Errorf("list active webhook configs: %w", err)
	}
	return r.openWebhookSecrets(cfgs)
}

// FindAll returns every config ordered by created_at, with secrets opened.
func (r *WebhookConfigRepository) FindAll(ctx context.Context) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := r.Query(ctx).Order("created_at").Find(&cfgs); err != nil {
		return nil, fmt.Errorf("list webhook configs: %w", err)
	}
	return r.openWebhookSecrets(cfgs)
}

// FindByID returns the config, or ErrRepositoryNotFound. The secret is opened.
func (r *WebhookConfigRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.WebhookConfig, error) {
	var cfg models.WebhookConfig
	if err := r.Query(ctx).Where("id = ?", id).First(&cfg); err != nil {
		return nil, fmt.Errorf("find webhook config: %w", err)
	}
	if cfg.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return r.openWebhookSecret(&cfg)
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
	return r.openWebhookSecrets(cfgs)
}

// UpdateFields writes columns on one config. A plaintext secret in fields is sealed
// for storage; the caller's map is left untouched.
func (r *WebhookConfigRepository) UpdateFields(ctx context.Context, id uuid.UUID, fields map[string]any) error {
	if id == uuid.Nil {
		return fmt.Errorf("update webhook config: id is required")
	}
	persisted, err := r.sealSecretField(fields)
	if err != nil {
		return fmt.Errorf("update webhook config: %w", err)
	}
	if _, err := r.Query(ctx).Model(&models.WebhookConfig{}).Where("id = ?", id).Update(persisted); err != nil {
		return fmt.Errorf("update webhook config: %w", err)
	}
	return nil
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

func (r *WebhookConfigRepository) sealSecretField(fields map[string]any) (map[string]any, error) {
	raw, hasSecret := fields[webhookSecretColumn]
	if !hasSecret {
		return fields, nil
	}
	plaintext, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("webhook config secret must be a string, got %T", raw)
	}
	sealed, err := settings.Seal(r.cipher, plaintext)
	if err != nil {
		return nil, err
	}
	persisted := make(map[string]any, len(fields))
	for key, value := range fields {
		persisted[key] = value
	}
	persisted[webhookSecretColumn] = sealed
	return persisted, nil
}

func (r *WebhookConfigRepository) openWebhookSecret(cfg *models.WebhookConfig) (*models.WebhookConfig, error) {
	plaintext, err := settings.Open(r.cipher, cfg.Secret)
	if err != nil {
		return nil, fmt.Errorf("webhook config %s secret: %w", cfg.ID, err)
	}
	cfg.Secret = plaintext
	return cfg, nil
}

func (r *WebhookConfigRepository) openWebhookSecrets(cfgs []models.WebhookConfig) ([]models.WebhookConfig, error) {
	for i := range cfgs {
		if _, err := r.openWebhookSecret(&cfgs[i]); err != nil {
			return nil, err
		}
	}
	return cfgs, nil
}
