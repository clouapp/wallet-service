package ramps

// HostedLinkDTO requests a hosted verification page.
//
// FullName comes from the caller because no local record holds one: the user
// row has a handle and an email, and the identity name only ever existed inside
// an application payload, which is deliberately not persisted. Email does come
// from the user record — it is authentication state and not the client's to
// assert.
type HostedLinkDTO struct {
	FullName     string
	RedirectURI  string
	Reason       string
	Endorsements []string
} //	@name	ClientHostedLinkDTO

// ApplicationDTO is the identity application as the client sent it, before
// anything is canonicalised. The single-provider and the fan-out routes both
// arrive here, and kyc.applicationFrom is what turns it into the contract every
// adapter reads.
type ApplicationDTO struct {
	SignedAgreementID string
	Endorsements      []string
	FirstName         string
	LastName          string
	MotherName        string
	Email             string
	Phone             string
	BirthDate         string
	TaxID             string
	// TaxIDType (cpf, passport, ssn, itin) and TaxIDCountry (the ISO country
	// that issued the document) as the client sent them; a build that predates them sends
	// neither, and models.DeriveTaxIdentity fills them from Country.
	TaxIDType    string
	TaxIDCountry string
	StreetLine1  string
	StreetLine2  string
	Number       string
	Neighborhood string
	City         string
	Subdivision  string
	PostalCode   string
	Country      string
	// Risk profile: keys of the vocabulary in
	// services/client/kyc/profile_vocabulary.go, not labels.
	Nationality               string
	Occupation                string
	SourceOfFunds             string
	AccountPurpose            string
	MonthlyIncomeBand         string
	ExpectedMonthlyVolumeBand string
	// IsPEP is a pointer so "false" and "absent" stay distinct — see
	// ramp.Profile.IsPEP.
	IsPEP *bool
} //	@name	ClientApplicationDTO

// StartTosDTO asks for the terms sessions still to sign, from every provider
// that serves the person. Country, TaxIDType and TaxIDCountry are the
// identity as the client declared it, all optional: a build that predates
// them sends none, and is offered every enabled provider's terms.
type StartTosDTO struct {
	RedirectURI  string
	Modes        []string
	Country      string
	TaxIDType    string
	TaxIDCountry string
} //	@name	ClientStartTosDTO

// DocumentsDTO is a documents submission as the client sent it: images as
// data URIs, any of them possibly absent.
type DocumentsDTO struct {
	DocumentType   string
	IssuingCountry string
	ImageFront     string
	ImageBack      string
	Selfie         string
	ProofOfAddress string
	// BankStatement is a PDF or an image, unlike the fields above.
	BankStatement string
	// ClientIPAddress is the address the request came from, set by the
	// controller from the trusted ClientIP middleware, as the request saw it;
	// the service canonicalises it. Never bound from a
	// body: a client must not choose the IP a provider records for it.
	ClientIPAddress string `json:"-"`
} //	@name	ClientDocumentsDTO
