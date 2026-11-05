package bridge

// businessCustomerRequest is the body of POST /customers and PUT
// /customers/{id} for a business customer (OpenAPI
// CreateBusinessCustomerPayload, read 18/10/2026). It is its own struct, so a
// person's body cannot change by a field added here. Every field is
// omitempty, a PUT being a patch, except the pointers that must go out as
// false.
type businessCustomerRequest struct {
	Type                   string                   `json:"type,omitempty"`
	BusinessLegalName      string                   `json:"business_legal_name,omitempty"`
	BusinessTradeName      string                   `json:"business_trade_name,omitempty"`
	BusinessType           string                   `json:"business_type,omitempty"`
	BusinessDescription    string                   `json:"business_description,omitempty"`
	BusinessIndustry       []string                 `json:"business_industry,omitempty"`
	Email                  string                   `json:"email,omitempty"`
	Phone                  string                   `json:"phone,omitempty"`
	PrimaryWebsite         string                   `json:"primary_website,omitempty"`
	IncorporationDate      string                   `json:"incorporation_date,omitempty"`
	IsDAO                  *bool                    `json:"is_dao,omitempty"`
	RegisteredAddress      *addressPayload          `json:"registered_address,omitempty"`
	PhysicalAddress        *addressPayload          `json:"physical_address,omitempty"`
	IdentifyingInformation []identifyingInformation `json:"identifying_information,omitempty"`
	AssociatedPersons      []associatedPersonBody   `json:"associated_persons,omitempty"`

	AccountPurpose             string `json:"account_purpose,omitempty"`
	AccountPurposeOther        string `json:"account_purpose_other,omitempty"`
	SourceOfFunds              string `json:"source_of_funds,omitempty"`
	EstimatedAnnualRevenueUSD  string `json:"estimated_annual_revenue_usd,omitempty"`
	ExpectedMonthlyPaymentsUSD *int   `json:"expected_monthly_payments_usd,omitempty"`
	ConductsMoneyServices      *bool  `json:"conducts_money_services,omitempty"`
	// MoneyServicesDescription and ComplianceScreeningExplanation are both
	// required beside a conducts_money_services of true.
	MoneyServicesDescription       string `json:"conducts_money_services_description,omitempty"`
	ComplianceScreeningExplanation string `json:"compliance_screening_explanation,omitempty"`

	SignedAgreementID string            `json:"signed_agreement_id,omitempty"`
	Endorsements      []string          `json:"endorsements,omitempty"`
	Documents         []documentPayload `json:"documents,omitempty"`
}

// associatedPersonBody is one person of a business, inline on the create and
// alone on POST/PUT /customers/{id}/associated_persons. Bridge requires the
// three role flags, so they go out as false.
type associatedPersonBody struct {
	FirstName                    string                   `json:"first_name,omitempty"`
	LastName                     string                   `json:"last_name,omitempty"`
	Email                        string                   `json:"email,omitempty"`
	Phone                        string                   `json:"phone,omitempty"`
	BirthDate                    string                   `json:"birth_date,omitempty"`
	ResidentialAddress           *addressPayload          `json:"residential_address,omitempty"`
	IdentifyingInformation       []identifyingInformation `json:"identifying_information,omitempty"`
	HasOwnership                 bool                     `json:"has_ownership"`
	HasControl                   bool                     `json:"has_control"`
	IsSigner                     bool                     `json:"is_signer"`
	IsDirector                   bool                     `json:"is_director"`
	OwnershipPercentage          *int                     `json:"ownership_percentage,omitempty"`
	Title                        string                   `json:"title,omitempty"`
	AttestedOwnershipStructureAt string                   `json:"attested_ownership_structure_at,omitempty"`
}

// personDocumentsBody is a person's documents alone, the PUT that places the
// photographed ID on an associated person.
type personDocumentsBody struct {
	IdentifyingInformation []identifyingInformation `json:"identifying_information"`
}

// associatedPersonRef is a person as a business customer lists them.
type associatedPersonRef struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}
