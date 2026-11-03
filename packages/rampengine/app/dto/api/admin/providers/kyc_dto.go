// Package providers contains DTOs for the admin KYC compliance explorer.
package providers

import (
	"time"

	customersdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/customers"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// AdminListKycDTO carries validated filter parameters for the cross-user
// compliance explorer. SyncedBefore is parsed in the controller from the query
// string before the DTO is built.
type AdminListKycDTO struct {
	Page     int    `json:"page"`
	PerPage  int    `json:"per_page"`
	Provider string `json:"provider"`
	// KycStatus is a provider row's status, or no_provider: the holders no
	// provider row names yet.
	KycStatus string `json:"kyc_status"`
	// CustomerType and CustomerStatus are the holder's.
	CustomerType      string     `json:"customer_type"`
	CustomerStatus    string     `json:"customer_status"`
	TosStatus         string     `json:"tos_status"`
	CollectionMode    string     `json:"collection_mode"`
	IntegrationStatus string     `json:"integration_status"`
	Rail              string     `json:"rail"`
	Search            string     `json:"q"`
	SyncedBefore      *time.Time `json:"synced_before,omitempty"`
	Sort              string     `json:"sort"`
	RejectionCategory string     `json:"rejection_category"`
	// Terminal and HasError are "true", "false" or empty (no filter), as the
	// query spelled them.
	Terminal string `json:"terminal"`
	HasError string `json:"has_error"`
} //	@name	AdminListKycDTO

// KycExplorerRowDTO carries one compliance explorer row between the service and
// controller layers. No json tags: this type is never serialised directly;
// controllers always pass through the resource layer.
type KycExplorerRowDTO struct {
	// Integration is nil on the row of a holder no provider row names yet.
	Integration *models.CustomerProviderIntegration
	Kyc         *models.CustomerProviderKyc
	// Customer is the holder; User is a person's account, nil for a company.
	Customer *models.Customer
	User     *models.User
	// LegalName is the holding company's; empty for a person.
	LegalName string
	// Members is who acts for the holder.
	Members customersdto.MembersSummaryDTO
} //	@name	AdminKycExplorerRowDTO
