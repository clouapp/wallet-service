package ramps

import dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"

// SubdivisionResource is one ISO 3166-2 subdivision, its code without the
// country prefix.
type SubdivisionResource struct {
	Code string `json:"code"`
	Name string `json:"name"`
} //	@name	ClientKycSubdivisionResource

// TaxIdentityOptionResource is one tax identity a resident may declare.
type TaxIdentityOptionResource struct {
	// Type is cpf, passport, ssn or itin.
	Type string `json:"type"`
	// Rails are the Pix rails it opens, pix_payin before pix_payout.
	Rails []string `json:"rails"`
	// DocumentTypes are the identity documents that prove it: national_id,
	// drivers_license, passport.
	DocumentTypes []string `json:"document_types"`
	// Providers are the ones that serve it (bridge, ozav): the app asks for a
	// step a provider requires, such as Ozav's bank statement, only when that
	// provider is among them.
	Providers []string `json:"providers"`
} //	@name	ClientKycTaxIdentityOptionResource

// BusinessTaxIdentityOptionResource is one tax identity a company
// incorporated in the country may declare.
type BusinessTaxIdentityOptionResource struct {
	// Type is cnpj, ein, crn, rfc, vat or other.
	Type string `json:"type"`
	// Rails are the Pix rails it opens, pix_payin before pix_payout.
	Rails []string `json:"rails"`
	// Providers are the business providers that serve it.
	Providers []string `json:"providers"`
	// DocumentPurposes are the company documents those providers want of a
	// company with a website, in the order formation, proof_of_address,
	// source_of_funds, tax_identification, nature_of_business: the KYB
	// documents step asks for exactly these.
	DocumentPurposes []string `json:"document_purposes"`
	// DocumentPurposesWithoutWebsite are the ones they want, in the same
	// order, of a company that declares no website ("A empresa não tem
	// site"): the documents step asks for these instead.
	DocumentPurposesWithoutWebsite []string `json:"document_purposes_without_website"`
} //	@name	ClientKycBusinessTaxIdentityOptionResource

// CountryResource is one served residence and what its form collects.
type CountryResource struct {
	// Code is ISO 3166-1 alpha-3; the app names the country from its locale.
	Code   string `json:"code"`
	Alpha2 string `json:"alpha2"`
	// FormProfile is br or generic.
	FormProfile string `json:"form_profile"`
	// PostalCodePattern is the anchored regular expression a postal code must
	// match; null when the country issues none.
	PostalCodePattern *string `json:"postal_code_pattern"`
	// Subdivisions are the ones an address must name; empty when it need not.
	Subdivisions  []SubdivisionResource       `json:"subdivisions"`
	TaxIdentities []TaxIdentityOptionResource `json:"tax_identities"`
	// BusinessTaxIdentities are what a company incorporated in the country
	// may declare; empty where no provider takes a company.
	BusinessTaxIdentities []BusinessTaxIdentityOptionResource `json:"business_tax_identities"`
	// CompanyTypes are the kinds of company the KYB form offers: mei, ei,
	// slu, ltda, sa or other in Brazil; corporation, llc, partnership,
	// sole_prop, cooperative, trust or other elsewhere. Empty where no
	// provider takes a company.
	CompanyTypes []string `json:"company_types"`
} //	@name	ClientKycCountryResource

// CountryCollection is GET /api/kyc/countries's whole body.
type CountryCollection struct {
	Data []CountryResource `json:"data"`
} //	@name	ClientKycCountryCollection

// NewCountryCollection projects the catalog; no country is an empty list.
func NewCountryCollection(countries []dto.CountryDTO) CountryCollection {
	data := make([]CountryResource, 0, len(countries))
	for _, country := range countries {
		data = append(data, NewCountryResource(country))
	}
	return CountryCollection{Data: data}
}

// NewCountryResource projects one country.
func NewCountryResource(country dto.CountryDTO) CountryResource {
	resource := CountryResource{
		Code: country.Code, Alpha2: country.Alpha2, FormProfile: country.FormProfile,
		Subdivisions:          make([]SubdivisionResource, 0, len(country.Subdivisions)),
		TaxIdentities:         taxIdentityOptions(country.TaxIdentities),
		BusinessTaxIdentities: businessTaxIdentityOptions(country.BusinessTaxIdentities),
		CompanyTypes:          append([]string{}, country.CompanyTypes...),
	}
	if country.PostalCodePattern != "" {
		pattern := country.PostalCodePattern
		resource.PostalCodePattern = &pattern
	}
	for _, subdivision := range country.Subdivisions {
		resource.Subdivisions = append(resource.Subdivisions, SubdivisionResource{Code: subdivision.Code, Name: subdivision.Name})
	}
	return resource
}

// taxIdentityOptions projects the options; none is an empty list.
func taxIdentityOptions(options []dto.TaxIdentityOptionDTO) []TaxIdentityOptionResource {
	out := make([]TaxIdentityOptionResource, 0, len(options))
	for _, option := range options {
		out = append(out, TaxIdentityOptionResource{
			Type: option.Type, Rails: option.Rails, DocumentTypes: option.DocumentTypes, Providers: option.Providers,
		})
	}
	return out
}

// businessTaxIdentityOptions projects a company's options; none is an empty
// list.
func businessTaxIdentityOptions(options []dto.BusinessTaxIdentityOptionDTO) []BusinessTaxIdentityOptionResource {
	out := make([]BusinessTaxIdentityOptionResource, 0, len(options))
	for _, option := range options {
		out = append(out, BusinessTaxIdentityOptionResource{
			Type: option.Type, Rails: option.Rails, Providers: option.Providers, DocumentPurposes: option.DocumentPurposes,
			DocumentPurposesWithoutWebsite: option.DocumentPurposesWithoutWebsite,
		})
	}
	return out
}
