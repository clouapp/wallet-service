package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WebhookSubscription struct {
	orm.Model
	ID                  uuid.UUID  `gorm:"type:uuid;primary_key"`
	ChainID             string     `gorm:"type:varchar(20);not null"`
	Provider            string     `gorm:"type:varchar(20);not null"`
	ProviderWebhookID   string     `gorm:"type:varchar(255);not null"`
	WebhookURL          string     `gorm:"type:text;not null"`
	SigningSecret       string     `gorm:"type:text;not null"`
	Status              string     `gorm:"type:varchar(20);default:active"`
	SyncStatus          string     `gorm:"type:varchar(20);default:synced"`
	SyncedAddressesHash *string    `gorm:"type:varchar(64)"`
	LastSyncedAt        *time.Time `gorm:"type:timestamptz"`
}

func (w *WebhookSubscription) TableName() string { return "webhook_subscriptions" }
