package ramps

// BusinessApplicationDTO is a KYB application as the client sent it (API
// contract §5.2): the company, its addresses and activity, and the people it
// names. The country of incorporation and the company's document are not in
// it: they are the draft's, and a resubmission cannot swap them.
type BusinessApplicationDTO struct {
	SignedAgreementID string
	LegalName         string
	TradeName         string
	CompanyType       string
	IncorporationDate string
	Email             string
	Phone             string
	// Website is empty when the company has none.
	Website           string
	Industry          string
	Description       string
	RegisteredAddress AddressDTO
	// PhysicalAddress is nil when the company operates at the registered one.
	PhysicalAddress     *AddressDTO
	AccountPurpose      string
	AccountPurposeOther string
	SourceOfFunds       string
	AnnualRevenueBand   string
	MonthlyVolumeBand   string
	// RegulatedActivity and ConductsMoneyServices are pointers: false is an
	// answer, and absent is a question nobody answered.
	RegulatedActivity        *bool
	ConductsMoneyServices    *bool
	MoneyServicesDescription string
	AssociatedPersons        []AssociatedPersonDTO
	OwnershipAttested        bool
} //	@name	ClientBusinessApplicationDTO

// AddressDTO is an address in the KYC form's fields.
type AddressDTO struct {
	StreetLine1  string
	StreetLine2  string
	Number       string
	Neighborhood string
	City         string
	Subdivision  string
	PostalCode   string
	Country      string
} //	@name	ClientAddressDTO

// AssociatedPersonDTO is one person of a KYB application, in the PF form's
// field names so the representative maps 1:1 from GET /api/kyc/profile.
type AssociatedPersonDTO struct {
	LocalRef   string
	FromMember bool
	Roles      []string
	// OwnershipPercent is the share as typed, a decimal string; empty when
	// none was sent.
	OwnershipPercent string
	Title            string
	FirstName        string
	LastName         string
	Email            string
	Phone            string
	BirthDate        string
	TaxID            string
	TaxIDType        string
	TaxIDCountry     string
	Nationality      string
	Address          AddressDTO
} //	@name	ClientAssociatedPersonDTO

// BusinessDocumentsDTO is a KYB application's documents (API contract §6.1):
// the company's by purpose and each person's identity document, by the
// person's local_ref.
type BusinessDocumentsDTO struct {
	CompanyDocuments []CompanyDocumentDTO
	PersonDocuments  []PersonDocumentDTO
} //	@name	ClientBusinessDocumentsDTO

// CompanyDocumentDTO is one of the company's documents: a purpose of the
// XIP vocabulary (ramp.CompanyDocument*) and its file, a data URI.
type CompanyDocumentDTO struct {
	Purpose string
	File    string
} //	@name	ClientCompanyDocumentDTO

// PersonDocumentDTO is one person's identity document.
type PersonDocumentDTO struct {
	LocalRef       string
	DocumentType   string
	IssuingCountry string
	ImageFront     string
	ImageBack      string
} //	@name	ClientPersonDocumentDTO
