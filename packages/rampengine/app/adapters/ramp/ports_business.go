package ramp

import (
	"slices"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// The purposes of a company's documents, in XIP's vocabulary (KYB spec
// §18.4): the app collects one file per purpose and each adapter names it in
// its provider's words.
const (
	// CompanyDocumentFormation is the company's constitution: a contrato
	// social or CCMEI in Brazil, a certificate of incorporation elsewhere.
	CompanyDocumentFormation = "formation"
	// CompanyDocumentProofOfAddress proves the registered address.
	CompanyDocumentProofOfAddress = "proof_of_address"
	// CompanyDocumentSourceOfFunds proves where the money comes from; a bank
	// statement serves.
	CompanyDocumentSourceOfFunds = "source_of_funds"
	// CompanyDocumentTaxIdentification proves the tax registration where the
	// country's registry does not (Mexico's RFC).
	CompanyDocumentTaxIdentification = "tax_identification"
	// CompanyDocumentNatureOfBusiness proves the company does what it says it
	// does — an invoice it issued, a contract with a client, an operating
	// licence — where no website shows it.
	CompanyDocumentNatureOfBusiness = "nature_of_business"
)

// CompanyDocumentPurposes lists every purpose once, in the order the app asks
// for them. A fresh slice: callers may keep it.
func CompanyDocumentPurposes() []string {
	return slices.Clone(companyDocumentPurposes)
}

var companyDocumentPurposes = []string{
	CompanyDocumentFormation, CompanyDocumentProofOfAddress,
	CompanyDocumentSourceOfFunds, CompanyDocumentTaxIdentification, CompanyDocumentNatureOfBusiness,
}

// BusinessKycAdapter is a provider that takes a business customer (KYB spec
// §7). It is optional, like ApplicationDocumentsRequirer: a provider that
// never heard of a company does not implement it, and the KYC listings, the
// fan-out and the jurisdiction policies serve a company only through one that
// does.
type BusinessKycAdapter interface {
	// BusinessApplicationDocuments names the company documents the provider
	// wants for a company incorporated in incorporation (ISO alpha-3) that
	// declares a website or none (hasWebsite), as CompanyDocument* purposes:
	// the app asks for exactly these. What the provider asks for afterwards
	// arrives as a pending step, as it does for a person.
	BusinessApplicationDocuments(incorporation string, hasWebsite bool) []string
}

// BusinessKycOf is adapter as a BusinessKycAdapter, and whether it is one.
func BusinessKycOf(adapter any) (BusinessKycAdapter, bool) {
	business, ok := adapter.(BusinessKycAdapter)
	return business, ok
}

// BusinessProfile is the company of a business application, declared in
// models for the reason Profile is (ports_kyc.go): the repository that keeps
// it takes the real struct, and it cannot import this package.
type BusinessProfile = models.BusinessProfile

// The roles an associated person holds in a company.
const (
	AssociatedPersonOwner    = "owner"
	AssociatedPersonControl  = "control"
	AssociatedPersonSigner   = "signer"
	AssociatedPersonDirector = "director"
)

// AssociatedPerson is someone a business application names: an owner of 25%
// or more, a person with control, a signer or a director.
type AssociatedPerson struct {
	// LocalRef is the app's id for the person, stable for the life of the
	// draft: the documents call and every retry point at it.
	LocalRef string
	// ExternalID is the provider's id for the person, from an earlier
	// submission's KycState.AssociatedPersonIDs; empty for a new one.
	ExternalID string
	// Representative is the member who registers the company: the one who
	// declares the ownership complete.
	Representative bool
	// Roles are AssociatedPerson* roles.
	Roles []string
	// OwnershipPercent is the share of an owner, 0 for anyone else.
	OwnershipPercent float64
	// Title is the position of a person with control.
	Title string
	// Profile is the person: names, e-mail, phone, birth date, the identity
	// they declare (TaxID, TaxIDType, TaxIDCountry) and where they live.
	Profile Profile
}

// HasRole reports whether the person holds role.
func (p AssociatedPerson) HasRole(role string) bool { return slices.Contains(p.Roles, role) }

// CompanyDocument is one of a company's documents: a CompanyDocument*
// purpose and the file, a `data:` URI with the PII handling of every other
// document image.
type CompanyDocument struct {
	Purpose string
	File    string
}

// PersonDocuments are the identity documents of one associated person.
type PersonDocuments struct {
	LocalRef string
	// ExternalID is the provider's id for the person, from the KycState of
	// the application that named them. A document for a person without one
	// cannot be placed, and the submission fails.
	ExternalID string
	// Documents are the person's identifying documents: the tax id they
	// declared, restated so the provider keeps it, and the photographed
	// document.
	Documents []IdentifyingDocument
}
