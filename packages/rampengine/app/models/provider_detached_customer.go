package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

// ProviderDetachedCustomer records a provider-side customer XIP detached from
// a customer's link to retry its KYC with a new provider-side customer
// (CustomerProviderIntegrationRepository.DetachCustomer). The provider keeps
// sending events about it; this is what tells those apart from events about a
// customer whose link is still on its way.
type ProviderDetachedCustomer struct {
	orm.Model
	CustomerID         uint      `gorm:"not null" json:"customer_id"`
	Provider           string    `gorm:"size:32;not null" json:"provider"`
	ExternalCustomerID string    `gorm:"size:128;not null" json:"external_customer_id"`
	DetachedAt         time.Time `gorm:"not null" json:"detached_at"`
}
