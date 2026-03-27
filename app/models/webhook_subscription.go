package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WebhookSubscription struct {
	orm.Model
	ID                  uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	ChainID             string     `gorm:"type:varchar(20);not null" json:"chain_id"`
	Provider            string     `gorm:"type:varchar(20);not null" json:"provider"`
	ProviderWebhookID   string     `gorm:"type:varchar(255);not null" json:"provider_webhook_id"`
	WebhookURL          string     `gorm:"type:text;not null" json:"webhook_url"`
	SigningSecret       string     `gorm:"type:text;not null" json:"-"`
	Status              string     `gorm:"type:varchar(20);default:active" json:"status"`
	SyncStatus          string     `gorm:"type:varchar(20);default:synced" json:"sync_status"`
	SyncedAddressesHash *string    `gorm:"type:varchar(64)" json:"-"`
	LastSyncedAt        *time.Time `gorm:"type:timestamptz" json:"last_synced_at,omitempty"`
}

func (w *WebhookSubscription) TableName() string { return "webhook_subscriptions" }
