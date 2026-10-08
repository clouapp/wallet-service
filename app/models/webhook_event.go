package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

const (
	WebhookDeliveryPending   = "pending"
	WebhookDeliveryDelivered = "delivered"
	WebhookDeliveryFailed    = "failed"
)

type WebhookEvent struct {
	orm.Model
	ID              uuid.UUID  `gorm:"type:uuid;primary_key"`
	TransactionID   *uuid.UUID `gorm:"type:uuid;index"`
	WebhookConfigID *uuid.UUID `gorm:"type:uuid"`
	SubjectID       *string    `gorm:"type:varchar(64)"`
	EventType       string     `gorm:"type:varchar(50);not null;index"`
	Payload         string     `gorm:"type:text;not null"`
	DeliveryURL     string     `gorm:"type:varchar(500);not null"`
	DeliveryStatus  string     `gorm:"type:varchar(20);not null;default:'pending';index"`
	Attempts        int        `gorm:"type:integer;not null;default:0"`
	MaxAttempts     int        `gorm:"type:integer;not null;default:10"`
	LastError       string     `gorm:"type:text"`
	DeliveredAt     *time.Time `gorm:"type:timestamptz"`
}

// TableName specifies the table name for WebhookEvent model
func (w *WebhookEvent) TableName() string {
	return "webhook_events"
}
