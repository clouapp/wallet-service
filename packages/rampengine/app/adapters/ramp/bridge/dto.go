package bridge

import "cmp"

// customerRequest is the body of both POST /customers and PUT /customers/{id}.
// Every field is omitempty because Bridge treats the PUT as a patch: restating
// a stale name would overwrite a correction made through another channel.
type customerRequest struct {
	Type      string `json:"type,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
	BirthDate string `json:"birth_date,omitempty"`
	// SourceOfFunds, AccountPurpose and AccountPurposeOther answer Bridge's
	// source_of_funds_questionnaire (profile_vocabulary.go). Empty, and so
	// absent, when the profile has no answer Bridge has a word for.
	SourceOfFunds          string                   `json:"source_of_funds,omitempty"`
	AccountPurpose         string                   `json:"account_purpose,omitempty"`
	AccountPurposeOther    string                   `json:"account_purpose_other,omitempty"`
	ResidentialAddress     *addressPayload          `json:"residential_address,omitempty"`
	SignedAgreementID      string                   `json:"signed_agreement_id,omitempty"`
	Endorsements           []string                 `json:"endorsements,omitempty"`
	IdentifyingInformation []identifyingInformation `json:"identifying_information,omitempty"`
	// Bridge accepts selfies only for a customer requesting the PIX
	// endorsement; otherwise they are a 400.
	LivenessCheckSelfies []livenessSelfie `json:"liveness_check_selfies,omitempty"`
	// Documents carries supporting files by purpose; XIP collects only the
	// proof of address.
	Documents []documentPayload `json:"documents,omitempty"`
}

// documentPayload is a data-URI file and the purposes it serves. Bridge wants a
// description only for purpose `other`, which XIP never sends.
type documentPayload struct {
	Purposes []string `json:"purposes"`
	File     string   `json:"file"`
}

type addressPayload struct {
	StreetLine1 string `json:"street_line_1,omitempty"`
	StreetLine2 string `json:"street_line_2,omitempty"`
	City        string `json:"city,omitempty"`
	Subdivision string `json:"subdivision,omitempty"`
	PostalCode  string `json:"postal_code,omitempty"`
	Country     string `json:"country,omitempty"` // ISO 3166 alpha-3
}

type livenessSelfie struct {
	Image string `json:"image"`
}

type identifyingInformation struct {
	Type           string `json:"type,omitempty"`
	IssuingCountry string `json:"issuing_country,omitempty"`
	Number         string `json:"number,omitempty"`
	ImageFront     string `json:"image_front,omitempty"`
	ImageBack      string `json:"image_back,omitempty"`
}

type customerListResponse struct {
	Data []customerResponse `json:"data"`
}

// customerResponse is what create, update and get all answer.
type customerResponse struct {
	ID string `json:"id"`
	// Type is individual or business: the rejection reads a business's own
	// codes only on a business.
	Type                      string                `json:"type"`
	Email                     string                `json:"email"`
	Status                    string                `json:"status"`
	Endorsements              []endorsementResponse `json:"endorsements"`
	RejectionReasons          []rejectionReason     `json:"rejection_reasons"`
	RequirementsDue           []string              `json:"requirements_due"`
	HasAcceptedTermsOfService bool                  `json:"has_accepted_terms_of_service"`
	// AssociatedPersons are a business customer's people, by id and e-mail.
	AssociatedPersons []associatedPersonRef `json:"associated_persons"`
}

type endorsementResponse struct {
	Name         string                  `json:"name"`
	Status       string                  `json:"status"`
	Requirements endorsementRequirements `json:"requirements"`
}

// endorsementRequirements is what an endorsement still needs. `missing` is
// absent, not empty, once nothing is missing. `pending` lists objects among
// its codes on a business customer ({"associated_person": id, "items": [...]},
// a partner's screenings), so it is read as requirementItems, which skips an
// object naming no code instead of failing the decode.
type endorsementRequirements struct {
	Issues  issueCodes           `json:"issues"`
	Pending requirementItems     `json:"pending"`
	Missing *missingRequirements `json:"missing"`
}

type missingRequirements struct {
	AllOf requirementItems `json:"all_of"`
	AnyOf requirementItems `json:"any_of"`
}

// rejectionReason carries a user-facing reason and Bridge's internal
// diagnostic; only the reason leaves this package (rejectionMessages).
type rejectionReason struct {
	Reason          string `json:"reason"`
	DeveloperReason string `json:"developer_reason"`
}

type tosLinkRequest struct {
	RedirectURI string `json:"redirect_uri,omitempty"`
}

// tosLinkResponse is the hosted terms page. The agreement id normally arrives
// on the redirect after signing, so it is usually empty here.
type tosLinkResponse struct {
	URL               string `json:"url"`
	SignedAgreementID string `json:"signed_agreement_id"`
}

// kycLinkRequest creates a hosted verification page and the customer behind it,
// the one path where a customer exists without an application.
type kycLinkRequest struct {
	FullName     string   `json:"full_name,omitempty"`
	Email        string   `json:"email,omitempty"`
	Type         string   `json:"type,omitempty"`
	Endorsements []string `json:"endorsements,omitempty"`
	RedirectURI  string   `json:"redirect_uri,omitempty"`
}

// kycLinkResponse covers both hosted-link endpoints: POST /kyc_links answers
// with the full object, GET /customers/{id}/kyc_link with just a url.
type kycLinkResponse struct {
	ID         string `json:"id"`
	CustomerID string `json:"customer_id"`
	KycLink    string `json:"kyc_link"`
	URL        string `json:"url"`
}

func (r kycLinkResponse) link() string { return cmp.Or(r.KycLink, r.URL) }
