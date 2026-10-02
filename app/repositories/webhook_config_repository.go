package repositories

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/security"
)

// WebhookConfigRepository stores webhook configs with the signing secret
// sealed at rest. Callers always see the plaintext secret: Create and
// UpdateFields seal it, every Find opens it.
type WebhookConfigRepository interface {
	Create(cfg *models.WebhookConfig) error
	FindByWalletID(walletID uuid.UUID) ([]models.WebhookConfig, error)
	FindByIDAndWallet(id, walletID uuid.UUID) (*models.WebhookConfig, error)
	FindActive() ([]models.WebhookConfig, error)
	FindAll() ([]models.WebhookConfig, error)
	FindByID(id uuid.UUID) (*models.WebhookConfig, error)
	FindVisibleToAccount(accountID uuid.UUID) ([]models.WebhookConfig, error)
	UpdateFields(id uuid.UUID, fields map[string]any) error
	Delete(cfg *models.WebhookConfig) error
	DeleteByID(id uuid.UUID) error
}

const webhookSecretColumn = "secret"

type webhookConfigRepository struct{}

func NewWebhookConfigRepository() WebhookConfigRepository {
	return &webhookConfigRepository{}
}

func (r *webhookConfigRepository) Create(cfg *models.WebhookConfig) error {
	if cfg == nil {
		return fmt.Errorf("webhook config is required")
	}
	plaintext := cfg.Secret
	sealed, err := security.SealSecret(facades.Crypt(), plaintext)
	if err != nil {
		return fmt.Errorf("webhook config secret: %w", err)
	}
	cfg.Secret = sealed
	createErr := facades.Orm().Query().Create(cfg)
	cfg.Secret = plaintext
	return createErr
}

func (r *webhookConfigRepository) FindByWalletID(walletID uuid.UUID) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := facades.Orm().Query().
		Where("wallet_id = ?", walletID).
		Find(&cfgs); err != nil {
		return nil, err
	}
	return openWebhookSecrets(cfgs)
}

func (r *webhookConfigRepository) FindByIDAndWallet(id, walletID uuid.UUID) (*models.WebhookConfig, error) {
	var cfg models.WebhookConfig
	err := facades.Orm().Query().
		Where("id = ? AND wallet_id = ?", id, walletID).
		First(&cfg)
	if err != nil {
		return nil, err
	}
	if cfg.ID == uuid.Nil {
		return nil, nil
	}
	return openWebhookSecret(&cfg)
}

func (r *webhookConfigRepository) FindActive() ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := facades.Orm().Query().
		Where("is_active", true).
		Find(&cfgs); err != nil {
		return nil, err
	}
	return openWebhookSecrets(cfgs)
}

func (r *webhookConfigRepository) FindAll() ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := facades.Orm().Query().Order("created_at").Find(&cfgs); err != nil {
		return nil, err
	}
	return openWebhookSecrets(cfgs)
}

func (r *webhookConfigRepository) FindByID(id uuid.UUID) (*models.WebhookConfig, error) {
	var cfg models.WebhookConfig
	if err := facades.Orm().Query().Where("id = ?", id).First(&cfg); err != nil {
		return nil, err
	}
	if cfg.ID == uuid.Nil {
		return nil, nil
	}
	return openWebhookSecret(&cfg)
}

// FindVisibleToAccount returns the account's own configs plus legacy configs
// that were created before ownership was recorded.
func (r *webhookConfigRepository) FindVisibleToAccount(accountID uuid.UUID) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	if err := facades.Orm().Query().
		Where("wallet_id IS NULL AND (account_id = ? OR account_id IS NULL)", accountID).
		Order("created_at").
		Find(&cfgs); err != nil {
		return nil, err
	}
	return openWebhookSecrets(cfgs)
}

func (r *webhookConfigRepository) UpdateFields(id uuid.UUID, fields map[string]any) error {
	if id == uuid.Nil {
		return fmt.Errorf("webhook config id is required")
	}
	if len(fields) == 0 {
		return fmt.Errorf("webhook config update fields are required")
	}
	persisted, err := sealSecretField(fields)
	if err != nil {
		return err
	}
	_, err = facades.Orm().Query().Model(&models.WebhookConfig{}).Where("id = ?", id).Update(persisted)
	return err
}

func (r *webhookConfigRepository) Delete(cfg *models.WebhookConfig) error {
	_, err := facades.Orm().Query().Delete(cfg)
	return err
}

func (r *webhookConfigRepository) DeleteByID(id uuid.UUID) error {
	_, err := facades.Orm().Query().Where("id = ?", id).Delete(&models.WebhookConfig{})
	return err
}

// sealSecretField returns the fields to persist, with a plaintext secret
// replaced by its sealed form. The caller's map is left untouched.
func sealSecretField(fields map[string]any) (map[string]any, error) {
	raw, hasSecret := fields[webhookSecretColumn]
	if !hasSecret {
		return fields, nil
	}
	plaintext, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("webhook config secret must be a string, got %T", raw)
	}
	sealed, err := security.SealSecret(facades.Crypt(), plaintext)
	if err != nil {
		return nil, fmt.Errorf("webhook config secret: %w", err)
	}
	persisted := make(map[string]any, len(fields))
	for key, value := range fields {
		persisted[key] = value
	}
	persisted[webhookSecretColumn] = sealed
	return persisted, nil
}

func openWebhookSecret(cfg *models.WebhookConfig) (*models.WebhookConfig, error) {
	plaintext, err := security.OpenSecret(facades.Crypt(), cfg.Secret)
	if err != nil {
		return nil, fmt.Errorf("webhook config %s secret: %w", cfg.ID, err)
	}
	cfg.Secret = plaintext
	return cfg, nil
}

func openWebhookSecrets(cfgs []models.WebhookConfig) ([]models.WebhookConfig, error) {
	for i := range cfgs {
		if _, err := openWebhookSecret(&cfgs[i]); err != nil {
			return nil, err
		}
	}
	return cfgs, nil
}
