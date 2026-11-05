package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

// WebhookEvent is an append-only record of each event a payment gateway sends.
// `(provider, event_id)` is the idempotency key — duplicate deliveries are
// dropped at insert. `external_ref` is the gateway's own resource id (e.g. the
// Astronpay order id) and is nullable for events that reference no order.
type WebhookEvent struct {
	orm.Model
	Provider        string     `gorm:"size:32;not null;index" json:"provider"`
	EventID         string     `gorm:"size:128;not null" json:"event_id"`
	EventType       string     `gorm:"size:64;not null" json:"event_type"`
	ExternalRef     *string    `gorm:"size:64;index" json:"external_ref,omitempty"`
	TransactionID   *uint      `gorm:"index" json:"transaction_id,omitempty"`
	ReceivedAt      time.Time  `gorm:"not null" json:"received_at"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
	ProcessingError *string    `gorm:"type:text" json:"processing_error,omitempty"`
	RawPayload      string     `gorm:"type:jsonb" json:"-"`
}

// Applied reports an event that ran to completion, which is the one state a
// re-dispatch must not apply again. A stamped failure is not applied: it is
// the row the admin explorer's retry exists for.
func (e *WebhookEvent) Applied() bool {
	return e.ProcessedAt != nil && e.ProcessingError == nil
}
