package models

import (
	"strings"

	"github.com/goravel/framework/database/orm"
)

// CustomerBusinessProfile is the company a business customer declared: one
// row per business, the whole declaration encrypted in Profile.
//
// No SoftDeletes, as CustomerIndividualProfile has none: the row lives and dies with its
// customer, whose own soft delete is what keeps the company's document held
// (customers.tax_id_hash, spec §18.1).
type CustomerBusinessProfile struct {
	orm.Model
	CustomerID uint `gorm:"not null;uniqueIndex" json:"customer_id"`
	// Profile is the JSON-encoded BusinessProfile, encrypted at rest with
	// facades.Crypt(). Never a document file: those are staged for the
	// provider call and never stored.
	Profile string `gorm:"type:text;not null" json:"-"`
}

// BusinessProfile is the company a business application declares (KYB spec
// §7, API contract §5.2), in XIP's vocabulary: the activity keys are
// kyc.Business*Keys and the company type a CompanyType*, which each adapter
// translates. ramp.BusinessProfile aliases it, for the reason
// ramp.Profile aliases KycProfile.
//
// A draft holds only what POST /api/customers took — the tax identity and
// number, and the names the registry answered with — until the first
// application replaces it with the whole form.
type BusinessProfile struct {
	// TaxIdentity is the company's tax id type and its country of
	// incorporation (ISO alpha-3), the pair the jurisdiction policies read.
	TaxIdentity TaxIdentity
	// TaxID is the number in its canonical form (upper case, no
	// punctuation): brazil.CanonicalDocument for a CNPJ.
	TaxID       string
	LegalName   string
	TradeName   string
	CompanyType string
	// IncorporationDate is YYYY-MM-DD.
	IncorporationDate string
	Email             string
	// Phone is E.164, as KycProfile.Phone is.
	Phone string
	// Website is empty when the company has none.
	Website     string
	Industry    string
	Description string
	// RegisteredAddress is where the company is incorporated;
	// PhysicalAddress where it operates, nil when that is the registered one.
	RegisteredAddress KycProfileAddress
	PhysicalAddress   *KycProfileAddress
	AccountPurpose    string
	// AccountPurposeOther is the free text beside an AccountPurpose "other".
	AccountPurposeOther string
	SourceOfFunds       string
	AnnualRevenueBand   string
	MonthlyVolumeBand   string
	RegulatedActivity   bool
	// ConductsMoneyServices is whether the company moves third parties'
	// money; MoneyServicesDescription is how, when it does.
	ConductsMoneyServices    bool
	MoneyServicesDescription string
	// OwnershipAttestedAt is when the representative declared the list of
	// owners complete (RFC 3339), the declaration Bridge takes in place of an
	// ownership document outside the EEA.
	OwnershipAttestedAt string
}

// cpfLength is the digits of a CPF, which an MEI's legal name ends with.
const cpfLength = 11

// DisplayName is what the company is called where others may see it — the
// wallet's name, a push on a lock screen, an e-mail (API contract §2.1): its
// trade name, else its legal name without a trailing run of exactly eleven
// digits. An MEI's legal name is its owner's name followed by their CPF, and
// none of those places may print a CPF.
func (p BusinessProfile) DisplayName() string {
	if trade := strings.TrimSpace(p.TradeName); trade != "" {
		return trade
	}
	legal := strings.TrimSpace(p.LegalName)
	digits := len(legal) - len(strings.TrimRight(legal, "0123456789"))
	if digits != cpfLength {
		return legal
	}
	return strings.TrimSpace(legal[:len(legal)-cpfLength])
}
