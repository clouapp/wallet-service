package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WebhookEvent struct {
	orm.Model
	ID             uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	TransactionID  *uuid.UUID `gorm:"type:uuid;index" json:"transaction_id,omitempty"`
	EventType      string     `gorm:"type:varchar(50);not null;index" json:"event_type"`
	Payload        string     `gorm:"type:text;not null" json:"payload"`
	DeliveryURL    string     `gorm:"type:varchar(500);not null" json:"delivery_url"`
	DeliveryStatus string     `gorm:"type:varchar(20);not null;default:'pending';index" json:"delivery_status"`
	Attempts       int        `gorm:"type:integer;not null;default:0" json:"attempts"`
	MaxAttempts    int        `gorm:"type:integer;not null;default:10" json:"max_attempts"`
	LastError      string     `gorm:"type:text" json:"last_error,omitempty"`
	DeliveredAt    *time.Time `gorm:"type:timestamp" json:"delivered_at,omitempty"`
}

// TableName specifies the table name for WebhookEvent model
func (w *WebhookEvent) TableName() string {
	return "webhook_events"
}
