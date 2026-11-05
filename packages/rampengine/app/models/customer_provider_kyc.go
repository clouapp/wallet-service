package models

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/goravel/framework/database/orm"
)

// Canonical XIP KYC statuses. Adapters map each provider's own vocabulary onto
// these; `raw_status` keeps the original value for the admin.
const (
	KycStatusNotStarted  = "not_started"
	KycStatusIncomplete  = "incomplete"
	KycStatusUnderReview = "under_review"
	KycStatusApproved    = "approved"
	KycStatusRejected    = "rejected"
	KycStatusPaused      = "paused"
	// KycStatusSubmitting is the one status no provider ever reports: the
	// application is staged and queued but has not reached the provider yet.
	// Document submission runs on a worker rather than in the request, so
	// without it the user would sit on `not_started` while their documents are
	// in flight — and an operator could not tell a stuck job from an untouched
	// account. Only the service sets it; an adapter returning it is a bug.
	KycStatusSubmitting   = "submitting"
	KycStatusRequiresInfo = "requires_info"
)

// AllKycStatuses lists every canonical KYC status, in the order a report should
// present them. It lives beside the constants so a new status reaches every
// consumer from one edit.
var AllKycStatuses = []string{
	KycStatusNotStarted, KycStatusIncomplete, KycStatusUnderReview,
	KycStatusApproved, KycStatusRejected, KycStatusPaused,
	KycStatusSubmitting, KycStatusRequiresInfo,
}

// How a provider collects identity documents. PreferredMode is what the
// adapter declares; CollectionMode is what is actually in use, and adds
// `hosted_fallback` for when the API-docs path was preferred but a hosted link
// had to be issued anyway.
const (
	KycModeAPIDocs            = "api_docs"
	KycModeHostedVerification = "hosted_verification"
	KycModeHostedFallback     = "hosted_fallback"
)

const (
	TosStatusPending  = "pending"
	TosStatusApproved = "approved"
	// TosStatusNotRequired is for a provider with no terms of service to sign.
	// Without it such a row would sit on TosStatusPending forever, which reads
	// as an outstanding step no client action will ever clear.
	TosStatusNotRequired = "not_required"
)

// Endorsement statuses, canonical across providers.
const (
	EndorsementStatusApproved   = "approved"
	EndorsementStatusPending    = "pending"
	EndorsementStatusIncomplete = "incomplete"
	EndorsementStatusRevoked    = "revoked"
	// EndorsementStatusNotAvailable is a capability the provider does not
	// offer where the customer lives (Bridge's
	// endorsement_not_available_in_customers_region: pix_onramp for anyone
	// outside Brazil). It is final: not pending, nothing the user sends
	// changes it, so it carries no requirement.
	EndorsementStatusNotAvailable = "not_available"
)

// Endorsement names. Providers grant either the umbrella `pix` endorsement or
// the direction-specific one, so both have to be recognised.
const (
	EndorsementPix        = "pix"
	EndorsementPixOnramp  = "pix_onramp"
	EndorsementPixOfframp = "pix_offramp"

	// EndorsementBankPayout opens the bank payout rails (ACH and wire) at a
	// provider that pays out to US bank accounts. One endorsement covers both
	// rails: the provider approves the customer for bank payouts, not per
	// settlement speed.
	EndorsementBankPayout = "bank_payout"
	// EndorsementBase is Bridge's approval to transact on the USD rails. It has
	// no umbrella and no direction: one grant clears the deposit and both
	// payouts.
	EndorsementBase = "base"
)

// RailEndorsement names the endorsements that can clear a rail: the one for
// that direction, and the umbrella that predates it.
type RailEndorsement struct {
	Directional string
	Umbrella    string
}

// railEndorsements maps a rail to its endorsements. The directional one
// decides wherever the provider reports it: Bridge deprecated the umbrella
// `pix` on 01/07/2026 and keeps it approved for a passport holder whose
// pix_onramp is not, so reading it next to the directional would open a
// deposit Bridge does not grant. The umbrella decides only a row with no
// directional at all — a customer from before them, or a provider (Ozav)
// that reports only the umbrella.
var railEndorsements = map[string]RailEndorsement{
	RailPixPayin:  {Directional: EndorsementPixOnramp, Umbrella: EndorsementPix},
	RailPixPayout: {Directional: EndorsementPixOfframp, Umbrella: EndorsementPix},
	// Bank rails have no umbrella: the empty name is skipped by railEndorsement.
	RailACHPayout:  {Directional: EndorsementBankPayout},
	RailWirePayout: {Directional: EndorsementBankPayout},
	// The USD rails have no umbrella: an empty name matches no endorsement.
	RailUSDPayin:      {Directional: EndorsementBase},
	RailUSDACHPayout:  {Directional: EndorsementBase},
	RailUSDWirePayout: {Directional: EndorsementBase},
}

// Endorsement is one provider capability grant and its state. Requirements
// lists what is still missing for it, which is what the /kyc requirements
// endpoint reports upward. The ramp package aliases this type — it must be
// declared here because models can never import ramp.
type Endorsement struct {
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Requirements []string `json:"requirements,omitempty"`
}

// IsApproved reports whether this endorsement currently clears its capability.
func (e Endorsement) IsApproved() bool { return e.Status == EndorsementStatusApproved }

// CustomerProviderKyc is the compliance state of one XIP customer at one
// provider. KYC at one provider never enables another, which is why this is
// per-provider rather than a column on the customer.
//
// There is deliberately no `rails_ready` column: the open rails are derived
// from `endorsements` by SupportsRail. A denormalized copy drifts from
// `endorsements` on every partial sync, and the router would read the stale
// value.
type CustomerProviderKyc struct {
	orm.Model
	orm.SoftDeletes
	CustomerID uint   `gorm:"not null;index" json:"customer_id"`
	Provider   string `gorm:"size:32;not null;index" json:"provider"`
	KycStatus  string `gorm:"size:24;not null;index" json:"kyc_status"`
	// NeedsApplication is not stored. The KYC service sets it on the rows it
	// reads, from whether the user holds a customer at the provider: a
	// provider with no customer and nothing in flight owes the user's whole
	// application, and nothing else on the row can say so — it names no
	// requirement, because nothing was ever sent that it could answer.
	NeedsApplication bool `gorm:"-" json:"-"`
	// ApplicationDocuments is not stored either. It is set beside
	// NeedsApplication, from the adapter: the supporting documents this
	// provider asks of every customer, in its own requirement vocabulary, so
	// a user owing the application is asked for them in the same pass.
	ApplicationDocuments []string `gorm:"-" json:"-"`
	// RailsNotEligible is not stored either. The KYC service sets it on the
	// rows it lists: the rails this provider does not open to the person the
	// user's retained profile declares (ramp.RailsOf), whatever the
	// endorsements clear — the router refuses them as rail_not_eligible
	// before it reads this row, and a listing that called them open would
	// promise what the next call refuses. Empty when nothing is refused or
	// nobody was declared.
	RailsNotEligible []string `gorm:"-" json:"-"`
	// Business is not stored either. The KYC service sets it on the rows of a
	// business customer, whose steps are the company's (models.KybSteps), not
	// a person's.
	Business bool `gorm:"-" json:"-"`
	// PreferredMode is what the adapter declares; CollectionMode is the mode in
	// force for this user.
	PreferredMode  string `gorm:"size:24;not null" json:"preferred_mode"`
	CollectionMode string `gorm:"size:24;not null" json:"collection_mode"`
	TosStatus      string `gorm:"size:16;not null" json:"tos_status"`
	// Hosted links and ToS URLs are text: they carry signed query strings well
	// past 255 characters.
	TosLinkURL *string `gorm:"type:text" json:"tos_link_url,omitempty"`
	// SignedAgreementID is the provider's proof that the terms were accepted,
	// and the submission gate reads its presence as exactly that. Nothing may
	// write it before an acceptance is recorded.
	SignedAgreementID *string `gorm:"size:64" json:"-"`
	// TosSessionID is a terms session a provider issued before it was
	// accepted, for the providers that work that way. It is kept apart from
	// SignedAgreementID precisely so an unaccepted session cannot pass for a
	// signature; on a successful acceptance it becomes one.
	//
	// A provider that issues it does so once and never returns it again, so
	// this column is the only copy — losing it means the user has to accept
	// against a fresh session.
	TosSessionID *string `gorm:"size:64" json:"-"`
	// TosExpiresAt is when that session stops being usable. It is what stands
	// between a user who left the wizard open overnight and a customer create
	// refused on a session that died while they slept.
	TosExpiresAt *time.Time `json:"-"`
	// TosAcceptedAt is when the user accepted, by our clock. The provider
	// stamps its own — we deliberately do not send one — so this is XIP's
	// record of the tick rather than the authoritative one.
	TosAcceptedAt   *time.Time `json:"-"`
	HostedKycLinkID *string    `gorm:"size:64" json:"hosted_kyc_link_id,omitempty"`
	HostedKycURL    *string    `gorm:"type:text" json:"hosted_kyc_url,omitempty"`
	// The jsonb columns are strings for the pgx reason documented in
	// models/transaction.go.
	Endorsements     string `gorm:"type:jsonb;not null" json:"-"`
	Requirements     string `gorm:"type:jsonb;not null" json:"-"`
	RejectionReasons string `gorm:"type:jsonb;not null" json:"-"`
	// RejectionCategories is why the provider refused, in XIP's vocabulary
	// (models.KycRejection*); empty unless the row is rejected. The adapter
	// derives it, so it is rewritten with every status the provider reports.
	RejectionCategories string `gorm:"type:jsonb;not null;default:'[]'" json:"-"`
	// RejectionDetails is the provider's own account of the same refusal —
	// Bridge's developer_reason, Ozav's codes — for the admin only: it names
	// vendor checks and never reaches the client.
	RejectionDetails string `gorm:"type:jsonb;not null;default:'[]'" json:"-"`
	// RawStatus is the provider's own snapshot. It carries customer PII and is
	// never logged and never returned to the client.
	// AssociatedPersonIDs is a business's people at the provider, by the
	// app's local_ref, as the last application left them (ramp.KycState):
	// the documents call and the next application place each person by it.
	// Null for a person's row. See PersonIDs.
	AssociatedPersonIDs *string    `gorm:"type:jsonb" json:"-"`
	RawStatus           string     `gorm:"type:jsonb;not null" json:"-"`
	LastSyncedAt        *time.Time `json:"last_synced_at,omitempty"`
	// The last submission that failed to reach the provider, kept on the row
	// because the request that queued it has long since answered 202. Without
	// it the failure lives only in the event stream — one frame, to whichever
	// app was listening at that second — and in the server log, which the
	// user cannot read and the admin has to go looking for. Code is the
	// stable contract code (ramp.ErrorCode) and is what the app shows; Detail
	// is the upstream text, for the admin only, since only this side knows
	// whether it restates customer data. All three are cleared by the next
	// submission that the provider accepts.
	LastErrorCode   *string    `gorm:"size:64" json:"-"`
	LastErrorDetail *string    `gorm:"type:text" json:"-"`
	LastErrorAt     *time.Time `json:"-"`
	// ApplicationDeferredAt is when an application to a provider that creates
	// its customer atomically was accepted and held for the documents. Without
	// it a row with no customer reads as one that never applied, and the user
	// would be asked for the whole application again. It goes back to nil once
	// the customer exists.
	ApplicationDeferredAt *time.Time `json:"-"`
}

// RecordSubmissionFailure notes why the last submission never took, so the
// row can say so after the event that announced it is gone.
func (k *CustomerProviderKyc) RecordSubmissionFailure(code, detail string) {
	now := time.Now()
	k.LastErrorCode = &code
	k.LastErrorDetail = &detail
	k.LastErrorAt = &now
}

// ClearSubmissionFailure forgets the last failure: the provider has accepted a
// submission since, and an old refusal shown next to an active review would
// read as a current one.
func (k *CustomerProviderKyc) ClearSubmissionFailure() {
	k.LastErrorCode, k.LastErrorDetail, k.LastErrorAt = nil, nil, nil
}

// SettleDocumentRefusal decides what a provider's answer leaves of a refused
// document. Such an answer says the customer still owes a document, not why, so
// while it does the refusal stands, with its category for the screen. Once the
// provider moves on from asking, the refusal is over.
func (k *CustomerProviderKyc) SettleDocumentRefusal() {
	if k.LastErrorCode == nil || *k.LastErrorCode != KycRejectionDocumentUnreadable {
		return
	}
	if k.KycStatus != KycStatusIncomplete && k.KycStatus != KycStatusRequiresInfo {
		k.ClearSubmissionFailure()
		return
	}
	if len(k.DecodedRejectionCategories()) == 0 {
		k.SetRejectionCategories([]string{KycRejectionDocumentUnreadable})
	}
}

// tosSessionMargin is how much life a stored terms session must have left to
// be worth reusing.
//
// The user still has to read the terms and tick the box after the session is
// handed to them, and the acceptance lands after that. A session that dies
// inside this window is one the customer create would refuse, so reusing it
// only moves the failure somewhere the user cannot act on it.
const tosSessionMargin = 5 * time.Minute

// HoldsLiveTosSession reports whether this row already has a terms session
// worth presenting again, so the provider is not asked for a second one.
//
// The reuse matters beyond politeness: a provider that issues the token once
// answers a repeat request with a session carrying no token at all, and that
// is a session nothing can be provisioned against.
func (k *CustomerProviderKyc) HoldsLiveTosSession(now time.Time) bool {
	if k.TosSessionID == nil || *k.TosSessionID == "" {
		return false
	}
	// No deadline given is no deadline to enforce; inventing one here would
	// throw away a session the provider still honours.
	if k.TosExpiresAt == nil {
		return true
	}
	return k.TosExpiresAt.After(now.Add(tosSessionMargin))
}

// ResetTos forgets a terms session the provider has refused, returning the row
// to the state it had before the step ran.
//
// It exists for one failure in particular. The submission is queued, so the
// refusal arrives long after the request that recorded the acceptance: the row
// is `approved`, holding an agreement id built from a session the provider no
// longer honours. Left alone, the terms step is skipped from then on — it only
// offers providers that are not approved — while every resubmission re-sends
// the same dead id and is refused identically, with nothing in the flow able to
// break the loop.
func (k *CustomerProviderKyc) ResetTos() {
	// A provider with no terms has no step to send anyone back to, and moving
	// such a row to pending would hold the wizard on one that never appears.
	if k.TosStatus == TosStatusNotRequired {
		return
	}
	k.TosStatus = TosStatusPending
	k.TosSessionID, k.SignedAgreementID = nil, nil
	k.TosLinkURL, k.TosExpiresAt, k.TosAcceptedAt = nil, nil, nil
}

// DecodedEndorsements decodes Endorsements. A malformed value yields nil, which
// SupportsRail reads as "no rail cleared" — the safe direction.
func (k *CustomerProviderKyc) DecodedEndorsements() []Endorsement {
	if k.Endorsements == "" {
		return nil
	}
	var list []Endorsement
	if err := json.Unmarshal([]byte(k.Endorsements), &list); err != nil {
		return nil
	}
	return list
}

// SetEndorsements encodes list into Endorsements.
func (k *CustomerProviderKyc) SetEndorsements(list []Endorsement) error {
	if list == nil {
		list = []Endorsement{}
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		return err
	}
	k.Endorsements = string(encoded)
	return nil
}

// DecodedRequirements decodes Requirements. A malformed value yields nil, which
// every caller reads as "nothing outstanding" — the same direction a missing
// column would take them.
func (k *CustomerProviderKyc) DecodedRequirements() []string {
	return decodeStringList(k.Requirements)
}

// DecodedRejectionReasons decodes RejectionReasons.
func (k *CustomerProviderKyc) DecodedRejectionReasons() []string {
	return decodeStringList(k.RejectionReasons)
}

// SetRequirements encodes list into Requirements.
//
// It lives on the model rather than beside either caller because both the sync
// path and the webhook path write this column, from different packages, and the
// column is what pending_steps is computed from. Two encoders for one column is
// how the two paths came to disagree about whether it gets written at all.
func (k *CustomerProviderKyc) SetRequirements(list []string) {
	k.Requirements = encodeStringList(list)
}

// SetRejectionReasons encodes list into RejectionReasons.
func (k *CustomerProviderKyc) SetRejectionReasons(list []string) {
	k.RejectionReasons = encodeStringList(list)
}

// DecodedRejectionCategories decodes RejectionCategories.
func (k *CustomerProviderKyc) DecodedRejectionCategories() []string {
	return decodeStringList(k.RejectionCategories)
}

// SetRejectionCategories encodes list into RejectionCategories.
func (k *CustomerProviderKyc) SetRejectionCategories(list []string) {
	k.RejectionCategories = encodeStringList(list)
}

// DecodedRejectionDetails decodes RejectionDetails.
func (k *CustomerProviderKyc) DecodedRejectionDetails() []string {
	return decodeStringList(k.RejectionDetails)
}

// SetRejectionDetails encodes list into RejectionDetails.
func (k *CustomerProviderKyc) SetRejectionDetails(list []string) {
	k.RejectionDetails = encodeStringList(list)
}

// RejectionTerminal reports whether this provider has refused the user for
// good: the row is rejected and any of its categories is terminal.
//
// A rejection carrying no category at all is not terminal. Every adapter
// names at least one for a rejection it maps — an unmapped reason is
// not_eligible — so an empty list is a row nothing ever classified, and it
// keeps the treatment it had before the categories existed rather than being
// shut out on a guess.
func (k *CustomerProviderKyc) RejectionTerminal() bool {
	if k.KycStatus != KycStatusRejected {
		return false
	}
	for _, category := range k.DecodedRejectionCategories() {
		if KycRejectionIsTerminal(category) {
			return true
		}
	}
	return false
}

// encodeStringList renders a list for a jsonb column that will not take an
// empty string.
func encodeStringList(list []string) string {
	if len(list) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func decodeStringList(encoded string) []string {
	if encoded == "" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(encoded), &list); err != nil {
		return nil
	}
	return list
}

// SupportsRail reports whether this provider's endorsements clear `rail` for
// this user. It is the single source of truth the router consults; nothing
// caches the answer.
func (k *CustomerProviderKyc) SupportsRail(rail string) bool {
	if k.KycStatus != KycStatusApproved {
		return false
	}
	e, ok := k.railEndorsement(rail)
	return ok && e.IsApproved()
}

// RailNotAvailable reports whether the provider has said `rail` is not
// offered where this user lives: the rail's directional endorsement is
// EndorsementStatusNotAvailable. Nothing else counts. The row does not know
// which tax identity the person declared, so an endorsement merely
// incomplete — even one asking only for a CPF, as Bridge's sandbox does for a
// passport holder — is not read as a refusal: for a CPF holder that would
// refuse a deposit with nothing to fix. A passport holder's deposit is
// refused before the row is read, from the identity (ramp.RailsOf).
func (k *CustomerProviderKyc) RailNotAvailable(rail string) bool {
	names, ok := railEndorsements[rail]
	if !ok {
		return false
	}
	for _, e := range k.DecodedEndorsements() {
		if e.Name == names.Directional {
			return e.Status == EndorsementStatusNotAvailable
		}
	}
	return false
}

// railEndorsement is the endorsement that decides `rail` for this row: the
// directional one when the provider reports it, the umbrella otherwise (see
// railEndorsements). False when the row carries neither, or the rail is
// unknown.
func (k *CustomerProviderKyc) railEndorsement(rail string) (Endorsement, bool) {
	names, ok := railEndorsements[rail]
	if !ok {
		return Endorsement{}, false
	}
	list := k.DecodedEndorsements()
	for _, name := range []string{names.Directional, names.Umbrella} {
		if name == "" {
			continue
		}
		for _, e := range list {
			if e.Name == name {
				return e, true
			}
		}
	}
	return Endorsement{}, false
}

// bridgeProviderOnlyRequirements are Bridge requirement codes that mean the
// provider is still working. They must not reopen an in-app wizard step — see
// kyc.nonUIMissingCodes — and an incomplete PIX endorsement that names only
// these is still "with the provider", like pending.
var bridgeProviderOnlyRequirements = map[string]struct{}{
	"external_account":     {},
	"account_processing":   {},
	"account_provisioning": {},
	"post_processing":      {},
	"manual_review":        {},
}

// AwaitingPixEndorsementReview reports whether the row is approved at the KYC
// level but a PIX rail the product uses is still with the provider. Bridge
// often grants SEPA while PIX is pending or incomplete for account
// provisioning; until PIX is approved the apps should read in_review, not
// approved with an empty rails_open. Incomplete endorsements count only when
// every listed requirement is provider-side — selfie and document codes mean
// the user still owes a wizard step. Each rail is read from the endorsement
// that decides it, so an umbrella still pending beside a directional that has
// already decided is not a review anything waits on. A rail the provider does
// not open to the person (RailsNotEligible) is not waited on either: the
// router refuses it before it reads the row, so a passport holder's deposit
// pending at Bridge would otherwise hold the overview in review forever.
func (k *CustomerProviderKyc) AwaitingPixEndorsementReview() bool {
	if k.KycStatus != KycStatusApproved {
		return false
	}
	for _, rail := range PixRails() {
		if slices.Contains(k.RailsNotEligible, rail) {
			continue
		}
		e, ok := k.railEndorsement(rail)
		if ok && pixEndorsementAwaitingProviderReview(e) {
			return true
		}
	}
	return false
}

func pixEndorsementAwaitingProviderReview(e Endorsement) bool {
	switch e.Status {
	case EndorsementStatusPending:
		return true
	case EndorsementStatusIncomplete:
		return incompleteEndorsementAwaitingProvider(e.Requirements)
	default:
		return false
	}
}

func incompleteEndorsementAwaitingProvider(requirements []string) bool {
	if len(requirements) == 0 {
		return false
	}
	for _, code := range requirements {
		if _, ok := bridgeProviderOnlyRequirements[code]; !ok {
			return false
		}
	}
	return true
}

// RailEndorsementFor returns the endorsements that can clear `rail`, or false
// when the rail is unknown.
func RailEndorsementFor(rail string) (RailEndorsement, bool) {
	names, ok := railEndorsements[rail]
	return names, ok
}

// KycPosture is what a client screen shows of the row: the status, the terms,
// what the provider still asks for and why it refused. A write that leaves it
// as it was is not news to an open screen; one that changes any of it is,
// whether or not the status moved — a corrigible rejection that gains a
// category, or a provider that asks for one more step, changes the wizard.
//
// The lists are compared decoded, so "[]" and an empty column read alike.
type KycPosture struct {
	Status              string
	TosStatus           string
	Requirements        string
	RejectionReasons    string
	RejectionCategories string
}

// Posture is the row's KycPosture.
func (k *CustomerProviderKyc) Posture() KycPosture {
	return KycPosture{
		Status:              k.KycStatus,
		TosStatus:           k.TosStatus,
		Requirements:        strings.Join(k.DecodedRequirements(), "\x00"),
		RejectionReasons:    strings.Join(k.DecodedRejectionReasons(), "\x00"),
		RejectionCategories: strings.Join(k.DecodedRejectionCategories(), "\x00"),
	}
}
