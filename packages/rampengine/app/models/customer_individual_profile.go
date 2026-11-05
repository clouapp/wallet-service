package models

import (
	"cmp"

	"github.com/goravel/framework/database/orm"
)

// CustomerIndividualProfile is the retained half of an individual customer's
// KYC submission: the profile fields (name, address, risk profile) its person
// typed, kept so a provider added later asks only for the photographs again.
//
// It carries no SoftDeletes on purpose. The photographs and identifying
// documents are never persisted at all (see ramp.Application), and the
// profile fields are the row's entire content — there is nothing left to keep
// once the account itself is gone, which is why the FK cascades on delete
// rather than leaving a trashed copy behind.
type CustomerIndividualProfile struct {
	orm.Model
	CustomerID uint `gorm:"not null;uniqueIndex" json:"customer_id"`
	// Profile is the JSON-encoded KycProfile, encrypted at rest with
	// facades.Crypt(). Never IdentifyingInformation, never
	// LivenessCheckSelfies — see CustomerIndividualProfileRepository for the boundary.
	Profile string `gorm:"type:text;not null" json:"-"`
	// TaxIDHash and PhoneHash are the keyed digests of the CPF and the phone
	// Profile declares, each unique across accounts; nil when it declares
	// none. CustomerIndividualProfileRepository derives both from the profile it stores,
	// so neither can disagree with it. A deleted account keeps TaxIDHash
	// alone (CustomerIndividualProfileRepository.Purge).
	TaxIDHash *string `gorm:"column:tax_id_hash;size:64" json:"-"`
	PhoneHash *string `gorm:"column:phone_hash;size:64" json:"-"`
}

// KycProfileAddress is a postal address; Country is ISO-3166 alpha-3.
// Declared here, not in app/adapters/ramp, for the same reason as
// Endorsement (user_provider_kyc.go): ramp.Address aliases this type, and
// models can never import ramp.
type KycProfileAddress struct {
	StreetLine1 string
	StreetLine2 string
	// Number is the street number ("1000", "s/n").
	Number string
	// Neighborhood is the bairro. It is required for Brazilian addresses at
	// providers that model it, and absent from providers that do not.
	Neighborhood string
	City         string
	Subdivision  string
	PostalCode   string
	Country      string
}

// KycProfile is the identity PII shared by every provider's customer
// payload — the exact fields ramp.Profile carries, aliased there
// (ramp.Profile = models.KycProfile) so CustomerIndividualProfileRepository can declare
// its interface with the real type rather than a JSON string.
//
// It is declared here rather than in ramp for the same reason as
// Endorsement: app/adapters/ramp already imports app/repositories (the
// router's own dependencies), so a repository importing ramp back would be a
// build cycle. Declaring the type in models — which repositories already
// imports everywhere, and which never imports ramp — breaks the cycle while
// keeping the repository's parameter a real struct an image can never reach,
// rather than a string that merely happens not to carry one today.
//
// BirthDate is `YYYY-MM-DD`. Phone is E.164 (`+5511987654321`).
type KycProfile struct {
	// TaxID is the tax id the user declared — a CPF or a passport number —
	// retained so a provider enabled later asks only for the photographs —
	// decided on 24/09/2026. It is only ever set on the retained copy: the
	// application a provider receives carries it in IdentifyingInformation, as
	// before, and no adapter reads it from here.
	TaxID string
	// TaxIDCountry is the country that issued TaxID (ISO alpha-3): BRA for a
	// CPF. It is kept apart from Address.Country because a person can declare
	// a CPF while living abroad, and a later application can move the address
	// and carry the tax id forward (kyc.checkIdentity). On a profile retained
	// before TaxIDType it is the residence the tax id was declared under,
	// which is what says whether it is a CPF (TaxIdentity); empty on one
	// retained before 28/09/2026, read by its Address.Country.
	TaxIDCountry string
	// TaxIDType is what TaxID is: cpf or passport (the TaxIDType* constants).
	// Declared apart from the residence because a person can hold a CPF, or
	// only a passport, while living abroad, and what a provider may be asked
	// depends on the document, not the address. Empty on profiles retained
	// before it existed, which are read through TaxIdentity (cpf for BRA,
	// passport otherwise).
	TaxIDType  string
	FirstName  string
	LastName   string
	LegalName  string
	MotherName string
	Email      string
	Phone      string
	BirthDate  string
	Address    KycProfileAddress
	// Nationality is ISO-3166 alpha-3, like Address.Country. Empty means the
	// client did not supply it.
	Nationality string
	// The five below are the risk profile Ozav asks of an individual.
	// Occupation is free text. SourceOfFunds and AccountPurpose are keys of
	// XIP's own vocabulary (see app/services/client/kyc/profile_vocabulary.go);
	// each adapter translates them into its provider's words.
	Occupation     string
	SourceOfFunds  string
	AccountPurpose string
	// Pointers, because nil ("not supplied") and 0 ("declared zero") are
	// different statements: Ozav reads 0 income as an answer.
	MonthlyIncomeUSD         *float64
	ExpectedMonthlyVolumeUSD *float64
	// IsPEP is a pointer for the opposite reason: an app build that never
	// asked the question must not declare `false` on the user's behalf. Ozav
	// treats an omitted value as false itself.
	IsPEP *bool
}

// TaxIdentity is the tax identity the profile declares, derived for profiles
// retained before TaxIDType existed the way they were read then: by the
// country the tax id was declared under, else the address's — Brazil is a
// CPF, anywhere else a passport. Not by the address first: a later
// application can move it abroad and carry a CPF forward (TaxIDCountry).
func (p KycProfile) TaxIdentity() TaxIdentity {
	return DeriveTaxIdentity(p.TaxIDType, p.TaxIDCountry, cmp.Or(p.TaxIDCountry, p.Address.Country))
}
