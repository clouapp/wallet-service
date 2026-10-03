package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WebhookConfig struct {
	orm.Model
	ID        uuid.UUID  `gorm:"type:uuid;primary_key"`
	URL       string     `gorm:"type:varchar(500);not null"`
	Secret    string     `gorm:"type:text;not null"`
	Events    string     `gorm:"type:text;not null"` // comma-separated event types
	IsActive  bool       `gorm:"type:boolean;not null;default:true;index"`
	WalletID  *uuid.UUID `gorm:"type:uuid;index"`
	AccountID *uuid.UUID `gorm:"type:uuid;index"`
	Type      string     `gorm:"type:varchar(50)"`
}

// TableName specifies the table name for WebhookConfig model
func (w *WebhookConfig) TableName() string {
	return "webhook_configs"
}
