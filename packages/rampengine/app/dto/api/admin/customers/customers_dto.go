// Package customers carries the admin's customers — who holds the money, a
// person or a company — between its controller and its service.
package customers

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// AdminListCustomersDTO carries the validated filters of the Clientes listing
// (KYB contract §12.2).
type AdminListCustomersDTO struct {
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
	Search  string `json:"q"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	// Provider alone selects customers holding a KYC row there; with
	// KycStatus, a row there in that status.
	Provider  string `json:"provider"`
	KycStatus string `json:"kyc_status"`
	Sort      string `json:"sort"`
} //	@name	AdminListCustomersDTO

// CustomerDTO is one customer as the admin lists it.
type CustomerDTO struct {
	Customer models.Customer
	// Business is what a company declared; nil for a person.
	Business *BusinessDTO
	// Owner is the user of the earliest membership holding the owner role —
	// a deleted account included; nil when no membership holds it.
	Owner        *models.User
	MembersCount int
	Kyc          []KycSummaryDTO
} //	@name	AdminCustomerDTO

// BusinessDTO is a company as the admin's listing shows it: never its whole
// document. An empty name is one nobody declared yet.
type BusinessDTO struct {
	LegalName            string
	TradeName            string
	CompanyType          string
	IncorporationCountry string
	TaxIDType            string
	// TaxIDMasked is a CNPJ in the Pix key's mask, any other document four
	// asterisks and its last four characters.
	TaxIDMasked string
} //	@name	AdminCustomerBusinessDTO

// KycSummaryDTO is one provider row of a customer, as the listing sums it up.
type KycSummaryDTO struct {
	Provider  string
	KycStatus string
	// Terminal is whether the provider refused the customer for good.
	Terminal bool
} //	@name	AdminCustomerKycSummaryDTO

// CustomerDetailDTO is the Cliente 360's overview: the listing's customer,
// its registered wallets and its members with their roles.
type CustomerDetailDTO struct {
	CustomerDTO
	Wallets []models.CustomerWallet
	Members []MemberDTO
	// KycOverall is where the customer's verification stands: the overall
	// status its app shows (kycsvc.Overall).
	KycOverall string
} //	@name	AdminCustomerDetailDTO

// MemberDTO is a membership, its user loaded, and the roles it holds.
type MemberDTO struct {
	Member models.CustomerMember
	Roles  []string
} //	@name	AdminCustomerMemberDTO

// MembersSummaryDTO is who acts for a customer, as the compliance explorer
// shows it: its owner — the user of the earliest membership holding the owner
// role, a deleted account included; nil when none holds it — and how many
// members it has.
type MembersSummaryDTO struct {
	Owner        *models.User
	MembersCount int
} //	@name	AdminCustomerMembersSummaryDTO

// MembershipDTO is one customer a user acts for, as the user page lists it.
type MembershipDTO struct {
	Customer models.Customer
	// LegalName is a company's; empty for a person and for a company that
	// declared none yet.
	LegalName string
	Roles     []string
	// KycOverall is as on CustomerDetailDTO.
	KycOverall string
} //	@name	AdminCustomerMembershipDTO

// BusinessProfileDTO is a company's profile as the Cliente 360 reads it,
// decrypted (KYB contract §12.4).
type BusinessProfileDTO struct {
	CustomerID uint
	// TaxIDReserved is whether the company holds its document: past draft,
	// until an admin releases it (spec §18.1).
	TaxIDReserved bool
	UpdatedAt     *time.Time
	Profile       models.BusinessProfile
	People        []BusinessPersonDTO
	// Documents is, per business provider the company applied to, what its
	// adapter declares and what it still names.
	Documents []BusinessDocumentsDTO
} //	@name	AdminCustomerBusinessProfileDTO

// BusinessPersonDTO is a person the company named and, per business provider
// it applied to, whether that provider knows them.
type BusinessPersonDTO struct {
	Person    models.BusinessPerson
	Providers []PersonProviderDTO
} //	@name	AdminCustomerBusinessPersonDTO

// PersonProviderDTO is whether provider holds an id for a person.
type PersonProviderDTO struct {
	Provider string
	Linked   bool
} //	@name	AdminCustomerPersonProviderDTO

// BusinessDocumentsDTO is the company documents a provider declares
// (Required) and still names (Missing), as XIP purposes.
type BusinessDocumentsDTO struct {
	Provider string
	Required []string
	Missing  []string
} //	@name	AdminCustomerBusinessDocumentsDTO
