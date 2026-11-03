// Package kyc contains the DTOs of the admin's hands on a user's retained KYC
// profile and the identity reservations it holds.
package kyc

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// KycProfileDTO is a user's retained KYC profile as an operator reads it.
// Profile is the zero value once it was purged with the account.
type KycProfileDTO struct {
	UserID        uint
	UpdatedAt     *time.Time
	Purged        bool
	TaxIDReserved bool
	PhoneReserved bool
	Profile       models.KycProfile
} //	@name	AdminKycProfileDTO

// UpdateKycProfileDTO is an operator's edit of the retained profile: a nil
// field is left as it is. TaxID or Phone set to "" clears it, and its
// reservation with it. TaxIDType (cpf, passport, ssn, itin) and TaxIDCountry say what
// TaxID is; left out, the profile keeps the identity it is read as.
type UpdateKycProfileDTO struct {
	TaxID                    *string
	TaxIDType                *string
	TaxIDCountry             *string
	FirstName                *string
	LastName                 *string
	LegalName                *string
	MotherName               *string
	Email                    *string
	Phone                    *string
	BirthDate                *string
	Address                  *KycProfileAddressDTO
	Nationality              *string
	Occupation               *string
	SourceOfFunds            *string
	AccountPurpose           *string
	MonthlyIncomeUSD         *float64
	ExpectedMonthlyVolumeUSD *float64
	IsPEP                    *bool
} //	@name	AdminUpdateKycProfileDTO

// KycProfileAddressDTO is a partial edit of the residential address.
type KycProfileAddressDTO struct {
	StreetLine1  *string
	StreetLine2  *string
	Number       *string
	Neighborhood *string
	City         *string
	Subdivision  *string
	PostalCode   *string
	Country      *string
} //	@name	AdminKycProfileAddressDTO

// ReleaseIdentityDTO names the reservations to release: "tax_id", "phone".
type ReleaseIdentityDTO struct {
	Fields []string
} //	@name	AdminReleaseIdentityDTO

// IdentityLookupDTO is one tax id, one phone or one company document to find
// the holder of. A tax id is read as its type (cpf, passport) and issuing
// country say; with neither it is a CPF, and with only a country, a tax id
// retained before its type was. A company document is read as TaxIDType and
// IncorporationCountry say.
type IdentityLookupDTO struct {
	TaxID                string
	TaxIDType            string
	TaxIDCountry         string
	Phone                string
	BusinessTaxID        string
	IncorporationCountry string
} //	@name	AdminIdentityLookupDTO

// IdentityHolderDTO is an account whose profile reserves the CPF or phone
// looked up, or a member of a company holding the document looked up, and
// which field matched. User may be soft-deleted. CustomerID is the holder: the
// account's own customer, or the company.
type IdentityHolderDTO struct {
	User       models.User
	CustomerID uint
	Field      string
} //	@name	AdminIdentityHolderDTO
