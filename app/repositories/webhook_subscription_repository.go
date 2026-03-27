package repositories

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"gorm.io/gorm"

	"github.com/macrowallets/waas/app/models"
)

type WebhookSubscriptionRepository interface {
	FindByChainID(chainID string) (*models.WebhookSubscription, error)
	FindByProviderAndChain(provider, chainID string) (*models.WebhookSubscription, error)
	FindAllActive() ([]models.WebhookSubscription, error)
	Create(sub *models.WebhookSubscription) error
	UpdateFields(id uuid.UUID, fields map[string]interface{}) error
}

type webhookSubscriptionRepository struct{}

func NewWebhookSubscriptionRepository() WebhookSubscriptionRepository {
	return &webhookSubscriptionRepository{}
}

func (r *webhookSubscriptionRepository) FindByChainID(chainID string) (*models.WebhookSubscription, error) {
	var sub models.WebhookSubscription
	err := facades.Orm().Query().Where("chain_id = ? AND status = ?", chainID, "active").First(&sub)
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *webhookSubscriptionRepository) FindByProviderAndChain(provider, chainID string) (*models.WebhookSubscription, error) {
	var sub models.WebhookSubscription
	err := facades.Orm().Query().Where("provider = ? AND chain_id = ?", provider, chainID).First(&sub)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if sub.ID == uuid.Nil {
		return nil, nil
	}
	return &sub, nil
}

func (r *webhookSubscriptionRepository) FindAllActive() ([]models.WebhookSubscription, error) {
	var subs []models.WebhookSubscription
	err := facades.Orm().Query().Where("status = ?", "active").Find(&subs)
	return subs, err
}

func (r *webhookSubscriptionRepository) Create(sub *models.WebhookSubscription) error {
	return facades.Orm().Query().Create(sub)
}

func (r *webhookSubscriptionRepository) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	_, err := facades.Orm().Query().
		Model(&models.WebhookSubscription{}).
		Where("id", id).
		Update(fields)
	return err
}
