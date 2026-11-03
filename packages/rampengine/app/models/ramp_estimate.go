package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

// RampEstimate is a persisted quote. It exists even for providers whose
// estimates are indicative, because it is what makes the provider sticky
// between estimate and create and what snapshots the fee that was quoted.
//
// The public identifier is ExternalID, not the primary key: orm.Model fixes the
// PK as uint, and `transactions` already established the ID + external_id
// pattern. Append-only, so no soft deletes — the `webhook_events` precedent.
type RampEstimate struct {
	orm.Model
	ExternalID string `gorm:"type:uuid;not null;uniqueIndex" json:"external_id"`
	// CustomerID is whose order the quote prices: only that customer redeems
	// it. UserID is the member who asked for it.
	CustomerID uint   `gorm:"not null;index" json:"customer_id"`
	UserID     uint   `gorm:"not null;index" json:"user_id"`
	Provider   string `gorm:"size:32;not null;index" json:"provider"`
	Rail       string `gorm:"size:24;not null" json:"rail"`
	// ProviderQuoteID is null for providers that return no upstream quote id.
	ProviderQuoteID *string `gorm:"size:64" json:"provider_quote_id,omitempty"`
	// Binding reports whether create must redeem this exact quote.
	Binding   bool       `gorm:"not null;default:false" json:"binding"`
	ExpiresAt *time.Time `gorm:"index" json:"expires_at,omitempty"`
	// ConsumedAt makes a binding quote single-use. Without it the same locked
	// quote creates two orders.
	ConsumedAt  *time.Time `json:"consumed_at,omitempty"`
	FeePercent  string     `gorm:"type:numeric(5,2);not null" json:"fee_percent"`
	RawRequest  string     `gorm:"type:jsonb;not null" json:"-"`
	RawResponse string     `gorm:"type:jsonb;not null" json:"-"`
	User        *User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// IsExpired reports whether the quote is past its expiry. An estimate with no
// expiry never expires.
func (e *RampEstimate) IsExpired() bool {
	return e.ExpiresAt != nil && e.ExpiresAt.Before(time.Now())
}

// IsConsumed reports whether this estimate already produced an order.
func (e *RampEstimate) IsConsumed() bool { return e.ConsumedAt != nil }

// IsUsable reports whether a create may still be priced from this estimate.
func (e *RampEstimate) IsUsable() bool { return !e.IsExpired() && !e.IsConsumed() }
