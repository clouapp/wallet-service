// Package companyregistry holds the DTOs of the client's CNPJ lookup.
package companyregistry

// CompanyDTO is what the Receita's registry says of a company, shaped for
// the KYB form it pre-fills (API contract §3). Every field is a suggestion
// the user confirms or corrects.
type CompanyDTO struct {
	TaxID     string
	LegalName string
	TradeName string
	// LegalNatureCode is the Concla code as the Receita writes it ("206-2"
	// is "2062"), LegalNatureDescription its name.
	LegalNatureCode        string
	LegalNatureDescription string
	// CompanyType is the models.CompanyType* the legal nature suggests, or
	// empty when no rule matches.
	CompanyType       string
	IncorporationDate string
	// RegistrationStatus is active, suspended, unfit, closed or void.
	RegistrationStatus string
	RegisteredAddress  AddressDTO
} //	@name	ClientCompanyRegistryCompanyDTO

// AddressDTO is a company's registered address in the KYB form's fields.
type AddressDTO struct {
	StreetLine1  string
	Number       string
	StreetLine2  string
	Neighborhood string
	City         string
	Subdivision  string
	PostalCode   string
	Country      string
} //	@name	ClientCompanyRegistryAddressDTO

// StandingDTO is what a business draft takes from the registry, read afresh.
// The zero value is a registry that could not say, which refuses nothing.
type StandingDTO struct {
	// Inactive is a company the registry holds suspended, unfit, closed or
	// void.
	Inactive bool
	// LegalName and TradeName are the names it is registered under; empty
	// when the registry did not answer.
	LegalName string
	TradeName string
} //	@name	ClientCompanyRegistryStandingDTO
