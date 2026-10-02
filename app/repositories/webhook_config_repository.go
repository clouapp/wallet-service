package repositories

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

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

type webhookConfigRepository struct{}

func NewWebhookConfigRepository() WebhookConfigRepository {
	return &webhookConfigRepository{}
}

func (r *webhookConfigRepository) Create(cfg *models.WebhookConfig) error {
	return facades.Orm().Query().Create(cfg)
}

func (r *webhookConfigRepository) FindByWalletID(walletID uuid.UUID) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	err := facades.Orm().Query().
		Where("wallet_id = ?", walletID).
		Find(&cfgs)
	return cfgs, err
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
	return &cfg, nil
}

func (r *webhookConfigRepository) FindActive() ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	err := facades.Orm().Query().
		Where("is_active", true).
		Find(&cfgs)
	return cfgs, err
}

func (r *webhookConfigRepository) FindAll() ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	err := facades.Orm().Query().Order("created_at").Find(&cfgs)
	return cfgs, err
}

func (r *webhookConfigRepository) FindByID(id uuid.UUID) (*models.WebhookConfig, error) {
	var cfg models.WebhookConfig
	if err := facades.Orm().Query().Where("id = ?", id).First(&cfg); err != nil {
		return nil, err
	}
	if cfg.ID == uuid.Nil {
		return nil, nil
	}
	return &cfg, nil
}

// FindVisibleToAccount returns the account's own configs plus legacy configs
// that were created before ownership was recorded.
func (r *webhookConfigRepository) FindVisibleToAccount(accountID uuid.UUID) ([]models.WebhookConfig, error) {
	var cfgs []models.WebhookConfig
	err := facades.Orm().Query().
		Where("wallet_id IS NULL AND (account_id = ? OR account_id IS NULL)", accountID).
		Order("created_at").
		Find(&cfgs)
	return cfgs, err
}

func (r *webhookConfigRepository) UpdateFields(id uuid.UUID, fields map[string]any) error {
	if id == uuid.Nil {
		return fmt.Errorf("webhook config id is required")
	}
	if len(fields) == 0 {
		return fmt.Errorf("webhook config update fields are required")
	}
	_, err := facades.Orm().Query().Model(&models.WebhookConfig{}).Where("id = ?", id).Update(fields)
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
