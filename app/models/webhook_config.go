package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WebhookConfig struct {
	orm.Model
	ID       uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	URL      string     `gorm:"type:varchar(500);not null" json:"url"`
	Secret   string     `gorm:"type:varchar(255);not null" json:"-"`
	Events   string     `gorm:"type:text;not null" json:"events"` // comma-separated event types
	IsActive bool       `gorm:"type:boolean;not null;default:true;index" json:"is_active"`
	WalletID *uuid.UUID `gorm:"type:uuid;index" json:"wallet_id,omitempty"`
	Type     string     `gorm:"type:varchar(50)" json:"type,omitempty"`
}

// TableName specifies the table name for WebhookConfig model
func (w *WebhookConfig) TableName() string {
	return "webhook_configs"
}
