package ramp

import (
	"encoding/json"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// TosOpts requests a terms-of-service session.
type TosOpts struct {
	RedirectURI string
	// IdempotencyKey is derived by the service from a value that survives a
	// retry; the adapter only forwards it upstream.
	IdempotencyKey string
}

// TosSession is a hosted terms-of-service page. SignedAgreementID is set only
// by providers that hand it back before the user signs.
type TosSession struct {
	URL               string
	SignedAgreementID string
	// SessionID is the provider's handle on this session, for providers that
	// issue one before it is accepted.
	//
	// It is kept apart from SignedAgreementID because the two answer different
	// questions, and conflating them is how an unaccepted session would pass
	// for a signature: SignedAgreementID means the user signed, and the
	// submission gate reads it as such, while this field means only that a
	// document is waiting. It becomes the agreement id — on the row, not here —
	// once the acceptance is recorded.
	//
	// A provider that issues it does so exactly once (Ozav keeps only its
	// hash), so an adapter must return it on the call that created the session
	// and the service must persist it there; there is no reading it back.
	SessionID string
	// ExpiresAt is when the session stops being usable, zero when the provider
	// sets no deadline. The service reuses a stored session only while this is
	// in the future, which is the only thing standing between a returning user
	// and a customer create refused on a session that died overnight.
	ExpiresAt time.Time
	// AllowedHosts are the hosts this session's page may legitimately load
	// from. The client renders it in a WebView with a JavaScript bridge
	// attached, so it needs an allowlist to enforce — and one provider's hosts
	// hardcoded in the client is what made every other provider's terms page
	// close in silence, with no error and no log.
	//
	// It travels on the session rather than being asked of the adapter
	// separately because it describes this page: the provider that produced the
	// URL is the only thing that knows where it may go.
	AllowedHosts []string
	Raw          json.RawMessage
}

// Address is a postal address; Country is ISO-3166 alpha-3.
//
// Number and Neighborhood are separate fields rather than part of StreetLine1
// because providers differ on whether they parse a street line: one that asks
// for them separately cannot recover them from a joined string, and a customer
// missing either sits incomplete with no error to read.
//
// It is an alias, not a copy: Profile below is itself an alias to
// models.KycProfile, whose Address field is models.KycProfileAddress, and Go
// requires the same underlying type on both sides of that field to assign
// directly. Declared in models — see Profile's own comment for why.
type Address = models.KycProfileAddress

// TaxIdentity is the declared tax id kind and its issuing country; the type
// constants are models.TaxIDType*.
type TaxIdentity = models.TaxIdentity

// Profile is the identity PII shared by every provider's customer payload.
// BirthDate is `YYYY-MM-DD`.
//
// Phone is E.164 (`+5511987654321`) — a single string, and the same one for
// every adapter. It is canonicalised once, in applicationFrom in the KYC
// service, so an adapter whose provider models the calling code separately
// splits it with SplitPhone (phone.go) instead of inventing its own reading.
// The format is stated here because leaving it unstated is what produced two
// different readings and a customer submitted with a blank country_code.
//
// It is an alias, not a copy — the same pattern as Endorsement below, and for
// the same reason: app/adapters/ramp already imports app/repositories (the
// router's own dependencies), so CustomerIndividualProfileRepository could not spell its
// parameter as ramp.Profile without a build cycle. Declaring the type in
// models — which repositories already imports, and which never imports ramp
// — lets the repository's interface use the real struct (an image is
// unrepresentable, not merely unmarshaled-away) while every caller here still
// writes `ramp.Profile`, because that name and `models.KycProfile` are the
// same type.
type Profile = models.KycProfile

// IdentifyingDocument carries either a number (CPF) or images (government ID).
// ImageFront/ImageBack are `data:` URIs and are PII: they are never logged and
// never persisted beyond an immediate retry. IssuingCountry is ISO alpha-3 in
// capitals; an adapter spells it as its provider takes it.
type IdentifyingDocument struct {
	Type           string
	IssuingCountry string
	Number         string
	ImageFront     string
	ImageBack      string
}

// Selfie is a liveness-check image, same handling rules as document images.
type Selfie struct {
	Image string
}

// Application is the canonical KYC submission. Adapters map it to their own
// customer payload; fields a provider does not accept are dropped there, not
// here.
type Application struct {
	// Type is `individual` or `business` (models.CustomerType*).
	Type                   string
	SignedAgreementID      string
	Endorsements           []string
	Profile                Profile
	IdentifyingInformation []IdentifyingDocument
	LivenessCheckSelfies   []Selfie
	// Business and AssociatedPersons are a business application's (Type
	// business), which carries no Profile: the company, and the people it
	// names, each with the identity and residence they declare.
	Business          *BusinessProfile
	AssociatedPersons []AssociatedPerson
	IdempotencyKey    string
}

// Documents is a follow-up submission for a customer that already exists.
type Documents struct {
	IdentifyingInformation []IdentifyingDocument
	LivenessCheckSelfies   []Selfie
	// ProofOfAddress is a `data:` image URI, same PII handling as the other
	// images. Empty means this submission carries none.
	ProofOfAddress string
	// BankStatement is a `data:` URI of a PDF or an image, same PII handling.
	// Only a provider that asks for one takes it; the others drop it, the way
	// Application fields a provider does not accept are dropped in its
	// adapter. Empty means this submission carries none.
	BankStatement string
	// CompanyDocuments and PersonDocuments are a business's: its documents
	// by purpose, and each associated person's identity documents.
	CompanyDocuments []CompanyDocument
	PersonDocuments  []PersonDocuments
	IdempotencyKey   string
	// ClientIPAddress is the IP of the request that sent the documents, as
	// the trusted ClientIP middleware resolved it. It is set by the
	// controller and never bound from the request body.
	ClientIPAddress string
}

// Endorsement is one provider capability and its state. It is an alias, not a
// copy: the same shape is persisted on `customer_provider_kycs`, and a second
// declaration would drift from it. The type is declared in models because
// models can never import ramp.
type Endorsement = models.Endorsement

// KycState is a provider's compliance snapshot, normalized. Status is a
// canonical XIP KYC status (see `app/models`), not the provider's own string —
// Raw preserves that for the admin.
type KycState struct {
	// ExternalCustomerID is set whenever the call materialized or confirmed a
	// customer id; the service persists it on the integration.
	ExternalCustomerID string
	Status             string
	// TosStatus is `pending` or `approved`.
	TosStatus         string
	SignedAgreementID string
	Endorsements      []Endorsement
	// Requirements and RejectionReasons are user-shareable messages.
	Requirements     []string
	RejectionReasons []string
	// RejectionCategories is why the provider refused, translated onto
	// models.KycRejection*; set only when Status is rejected, and never empty
	// then — a reason the adapter cannot map is not_eligible. The adapter owns
	// the translation, so the service and the API never learn a provider's
	// vocabulary.
	RejectionCategories []string
	// RejectionDetails is the provider's own account of the refusal (Bridge's
	// developer_reason, Ozav's reason codes), for the admin only.
	RejectionDetails []string
	// AssociatedPersonIDs are a business customer's people at the provider,
	// by AssociatedPerson.LocalRef, as the call that named them answered:
	// the service keeps them for the documents and the next resubmission.
	AssociatedPersonIDs map[string]string
	// CustomerEmail is the email the provider holds for this customer.
	//
	// It is here for one caller: the admin relink, which repoints a user at a
	// provider-side customer id typed by hand. A wrong id is not a typo that
	// fails — it succeeds, and every refresh afterwards writes another
	// person's status, requirements and rejection reasons onto this user's
	// row, which their app then renders. The email is what lets the service
	// refuse before writing, and it is the same comparison SubmitApplication
	// already makes against the account.
	//
	// Empty when the provider did not report one; the relink reads that as
	// "cannot prove ownership" and refuses, rather than assuming a match.
	CustomerEmail string
	// Raw is the provider snapshot. It carries PII and is never logged.
	Raw json.RawMessage
}

// KycRequirements is what the user still has to provide.
type KycRequirements struct {
	Status       string
	Missing      []string
	Endorsements []Endorsement
	Raw          json.RawMessage
}

// HostedLinkOpts requests a hosted verification page — the primary flow for
// some providers and the fallback for others.
type HostedLinkOpts struct {
	// FullName, Email and Type are needed only when the hosted link is what
	// materializes the customer, which is how a user reaches the hosted path
	// without ever submitting an application. The service reads them from the
	// user record; a provider whose link always targets an existing customer
	// ignores them.
	FullName     string
	Email        string
	Type         string
	Endorsements []string
	Capabilities []string
	RedirectURI  string
	// Reason records why the link was issued (`primary_flow`,
	// `api_submit_failed`, `support_request`) for the admin trail.
	Reason         string
	IdempotencyKey string
}

// HostedKycLink is an issued hosted verification page. CollectionMode tells the
// service what to persist: the provider's primary flow or a fallback.
type HostedKycLink struct {
	LinkID string
	URL    string
	// ExternalCustomerID is set when issuing the link also created the
	// customer, which is how the hosted path materializes one.
	ExternalCustomerID string
	CollectionMode     CollectionMode
	Raw                json.RawMessage
}

// --- Integration ------------------------------------------------------------

// Integration is the provider-side customer link. Status is one of the
// integration statuses in `app/models`.
type Integration struct {
	ExternalCustomerID string
	Status             string
	Metadata           map[string]any
	Raw                json.RawMessage
}
