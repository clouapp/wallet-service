package models

import "github.com/goravel/framework/database/orm"

// A customer's lifecycle as a holder. Approval is not here: it is per provider,
// on each provider's KYC row. An individual is born active; a business is born
// a draft and becomes active on its first approval.
const (
	CustomerStatusDraft  = "draft"
	CustomerStatusActive = "active"
	CustomerStatusClosed = "closed"
)

// BusinessCustomerIDFloor is the first id of a business, the start of
// customers_business_id_seq: every id below it is an individual's, whose id is
// its user's.
const BusinessCustomerIDFloor = 1_000_000_000_000

// IsIndividualCustomerID reports whether id is an individual customer's — the
// same number as its user's — rather than a business's, which takes its id
// from the business range.
func IsIndividualCustomerID(id uint) bool { return id < BusinessCustomerIDFloor }

// Customer is who holds money at XIP — a person or a company, its Type one of
// CustomerType* — kept apart from who logs in (User), which is the providers'
// own model.
//
// The id has no default. An individual customer always takes its user's id, so
// every key already sent to a provider (va:%d:…, kyc-link:%d) stays the same
// for it and the request without a customer header resolves it without a
// query. A business takes the next value of customers_business_id_seq, which
// starts at 10^12, so it never meets a user's id.
type Customer struct {
	orm.Model
	orm.SoftDeletes
	Type   string `gorm:"size:16;not null" json:"type"`
	Status string `gorm:"size:16;not null" json:"status"`
	// TenantKey is Macro's account UUID. Applicant IDs stay internal to the
	// ported engine; external callers address the tenant-scoped UUID resource.
	TenantKey   string `gorm:"size:36;not null;index" json:"-"`
	Environment string `gorm:"size:16;not null" json:"-"`
	// TaxIDHash is the keyed digest of a business's tax document: unique among
	// the businesses that left draft, so a draft never holds a document for
	// good. Null for an individual, whose documents live in its KYC profile.
	TaxIDHash *string `gorm:"size:64" json:"-"`
}

// IsBusiness reports whether the customer is a company, held to the money
// rules a person is not (spec §9).
func (c *Customer) IsBusiness() bool { return c.Type == CustomerTypeBusiness }
