package ramps

import "github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"

// CountryDTO is one country of the KYC catalog: a residence some eligible
// provider serves, and what the app's form needs to collect there.
type CountryDTO struct {
	// Code is ISO 3166-1 alpha-3, Alpha2 the same country in alpha-2.
	Code   string
	Alpha2 string
	// FormProfile is the form the app renders: "br" or "generic".
	FormProfile string
	// PostalCodePattern is the anchored RE2 a postal code must match; empty
	// for a country that issues none.
	PostalCodePattern string
	// Subdivisions are the ones an address must name; empty when it need not.
	Subdivisions  []iso3166.Subdivision
	TaxIdentities []TaxIdentityOptionDTO
	// BusinessTaxIdentities are what a company incorporated in the country
	// may declare (KYB spec §7.2); empty where no provider takes a company.
	BusinessTaxIdentities []BusinessTaxIdentityOptionDTO
	// CompanyTypes are the kinds of company the KYB form offers there: the
	// Brazilian natures in Brazil, Bridge's vocabulary elsewhere; empty
	// where no provider takes a company.
	CompanyTypes []string
} //	@name	ClientKycCountryDTO

// TaxIdentityOptionDTO is one tax identity a resident may declare, the Pix
// rails it opens, the identity documents that prove it and the providers that
// serve it.
type TaxIdentityOptionDTO struct {
	// Type is cpf, passport, ssn or itin.
	Type string
	// Rails are pix_payin and pix_payout, in that order, whichever are open.
	Rails []string
	// DocumentTypes are national_id, drivers_license or passport.
	DocumentTypes []string
	// Providers are the ones that open it some rail, in ramp.AllProviderCodes
	// order: a step only some provider asks for (Ozav's bank statement) is
	// owed only when one of these asks for it.
	Providers []string
} //	@name	ClientKycTaxIdentityOptionDTO

// BusinessTaxIdentityOptionDTO is one tax identity a company may declare, the
// Pix rails it opens, the business providers that serve it and the company
// documents they want.
type BusinessTaxIdentityOptionDTO struct {
	// Type is cnpj, ein, crn, rfc, vat or other.
	Type string
	// Rails are pix_payin and pix_payout, in that order, whichever are open.
	Rails     []string
	Providers []string
	// DocumentPurposes are the company documents the providers want of a
	// company with a website, in ramp.CompanyDocumentPurposes order: the KYB
	// form asks for exactly these.
	DocumentPurposes []string
	// DocumentPurposesWithoutWebsite are the ones they want of a company that
	// declares none, in the same order.
	DocumentPurposesWithoutWebsite []string
} //	@name	ClientKycBusinessTaxIdentityOptionDTO
