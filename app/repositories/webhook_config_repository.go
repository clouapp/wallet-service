package repositories

import (
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

func (r *webhookConfigRepository) Delete(cfg *models.WebhookConfig) error {
	_, err := facades.Orm().Query().Delete(cfg)
	return err
}

func (r *webhookConfigRepository) DeleteByID(id uuid.UUID) error {
	_, err := facades.Orm().Query().Delete(&models.WebhookConfig{}, id)
	return err
}
