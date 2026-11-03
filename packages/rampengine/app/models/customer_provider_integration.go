package models

import "github.com/goravel/framework/database/orm"

const (
	IntegrationStatusPending  = "pending"
	IntegrationStatusActive   = "active"
	IntegrationStatusDisabled = "disabled"
)

// CustomerProviderIntegration links a XIP customer to its customer on one
// provider. It replaces the role `receivers` played for Astronpay.
//
// `status=active` means "provider customer linked and usable"; which rails the
// customer may actually transact on depends on CustomerProviderKyc, not on
// this row.
//
// The (customer_id, provider) unique index is partial (WHERE deleted_at IS NULL):
// disabling an integration and re-linking is a real flow, and a plain Postgres
// unique ignores deleted_at, so a soft-deleted row would block the pair
// forever — the trap `receivers` carries today.
type CustomerProviderIntegration struct {
	orm.Model
	orm.SoftDeletes
	CustomerID uint   `gorm:"not null;index" json:"customer_id"`
	Provider   string `gorm:"size:32;not null;index" json:"provider"`
	// ExternalCustomerID is null until a submit or a hosted link materializes
	// the customer upstream.
	ExternalCustomerID *string `gorm:"size:64" json:"external_customer_id,omitempty"`
	Status             string  `gorm:"size:16;not null;index" json:"status"`
	// Metadata is auxiliary provider data, jsonb-compatible text.
	Metadata string `gorm:"type:jsonb;not null" json:"-"`
	// User is the account of an individual customer, whose id the customer
	// took; the admin explorer preloads it. A business has none.
	User *User `gorm:"foreignKey:CustomerID" json:"user,omitempty"`
	// Customer is the holder the link is for; the admin explorer preloads it.
	Customer *Customer `gorm:"foreignKey:CustomerID" json:"-"`
}

// IsActive reports whether the integration itself is enabled.
func (i *CustomerProviderIntegration) IsActive() bool { return i.Status == IntegrationStatusActive }

// IsUsable reports whether the router may route through this integration: it
// must be active and carry the provider-side customer id every adapter call
// needs.
func (i *CustomerProviderIntegration) IsUsable() bool {
	return i.IsActive() && i.ExternalCustomerID != nil && *i.ExternalCustomerID != ""
}
