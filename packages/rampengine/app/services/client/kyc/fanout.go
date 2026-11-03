package kyc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

// SubmissionOutcome is one provider's answer to a fan-out submission.
//
// It carries the error rather than discarding it because partial success is
// the normal case, not the exception: one provider accepting and another
// refusing must leave the user usable by the one that worked, and must still
// say what happened at the other.
type SubmissionOutcome struct {
	Provider string
	Row      *models.CustomerProviderKyc
	Err      error
	// Skipped marks a provider that had nothing to gain from this submission.
	//
	// It is a third outcome, not a flavour of either other one: collapsing it
	// into a refusal puts an error on screen for a request where nothing went
	// wrong, and collapsing it into an acceptance claims a job was queued that
	// never was.
	Skipped    bool
	SkipReason string
}

// Accepted reports whether this provider took the submission. A skipped
// provider took nothing and refused nothing.
func (o SubmissionOutcome) Accepted() bool { return o.Err == nil && !o.Skipped }

// OutstandingFor is everything a provider has said about what is missing.
//
// Requirements and rejection reasons are separate projections upstream — Bridge
// can refuse a customer with "cannot validate ID" and an empty requirements_due
// — and the phrase table in retry_steps.go exists to read the second. Passing
// only the first left that table unreachable: the wizard showed a rejected user
// nothing to fix, and the redistribution filter, asking the same question over
// the same partial evidence, then skipped the provider for good.
//
// The rejection categories are part of it: a provider that refused for a
// reason the user can fix owes the steps that reason reopens
// (PendingStepsFromMissing reads the category codes), which is what sends it
// the resubmission of those steps and nothing else.
func OutstandingFor(row *models.CustomerProviderKyc) []string {
	requirements := row.DecodedRequirements()
	rejections := row.DecodedRejectionReasons()
	categories := row.DecodedRejectionCategories()
	out := make([]string, 0, len(requirements)+len(rejections)+len(categories))
	out = append(out, requirements...)
	out = append(out, rejections...)
	return append(out, categories...)
}

// ShouldSkipRedistribution reports whether a provider has nothing to gain from
// a *document* resubmission specifically — SubmitApplicationToEnabled passes
// no skip check at all, because the application PATCH can satisfy any step.
//
// The question is close to, but narrower than, the one the wizard asks the
// user. The wizard's "is there anything left you can act on?" spans every
// step; this one only cares whether a step a document call can satisfy is
// still open, via DocumentSteps. That narrowing is the fix for the case
// measured live on 03/09: 20 approved Ozav customers whose only outstanding
// items are risk-profile fields (`missing_fields: [monthly_income_usd,
// is_pep]` and the like) — real, current requirements, satisfied by the
// application endpoint, not this one. Reading the full pending-step list here
// would call that "something outstanding" and re-send their identity
// documents on every retry meant for another provider, regressing an approved
// customer to under_review for nothing this call could ever fix.
//
// Note it reads pending steps rather than raw requirements even after
// DocumentSteps narrows them. A provider can report outstanding risk-profile
// fields on a customer it has fully approved — Ozav documents exactly that,
// from measurement — so "requirements is empty" would be false forever there
// and the filter would never fire.
//
// not_started is the exception that makes the rule usable at all: on a first
// submission nobody owes anything yet, and without this line the very first
// application would reach no provider.
func ShouldSkipRedistribution(status string, requirements []string) bool {
	return shouldSkipRedistribution(status, requirements, PendingStepsFromMissing)
}

// ShouldSkipBusinessRedistribution is ShouldSkipRedistribution for a
// business's provider: the same rule, over the company's steps
// (BusinessPendingStepsFromMissing), of which a documents call answers the
// people's IDs (owners) and the company's documents.
func ShouldSkipBusinessRedistribution(status string, requirements []string) bool {
	return shouldSkipRedistribution(status, requirements, BusinessPendingStepsFromMissing)
}

// shouldSkipRedistribution is the rule both read, steps mapping the
// provider's codes onto the wizard's.
func shouldSkipRedistribution(status string, requirements []string, steps func([]string) []string) bool {
	switch status {
	case models.KycStatusNotStarted:
		return false
	// An application is in flight, and these documents belong to the same pass
	// of the wizard: they are queued behind it, not weighed against it. The
	// provider owes nothing yet only because it has not been told anything yet
	// — skipping here would mean the user's documents never reach it at all.
	case models.KycStatusSubmitting:
		return false
	// Analysing the set it already has. A second set restarts that review.
	case models.KycStatusUnderReview:
		return true
	// A suspended account must not be reopened by a resubmission.
	case models.KycStatusPaused:
		return true
	// Naming nothing at all is not the same statement as naming something that
	// does not gate, and collapsing the two is what stranded a real customer.
	//
	// Measured on 03/09: with the risk-profile fields filled, Ozav answers
	// `missing_fields: []` and holds the record at `pending` with
	// `verification.checks` empty — it has verified nothing, because it has
	// received no documents. It cannot ask for them, because its missing-fields
	// vocabulary is a list of paths on the customer object and documents are
	// not one. So the row says `incomplete` and owes nothing, the filter reads
	// that as "nothing to gain", and the documents are dropped: the provider
	// waits forever for what our own filter refuses to send.
	//
	// This exception is only for the row that names nothing while saying it is
	// not finished, so it reads the full pending-step list rather than
	// DocumentSteps' narrower one: the distinction it exists for is "named
	// nothing" versus "named something", not which steps that something maps
	// to. A row that names only risk-profile fields here still receives,
	// unlike the default case below — this status has not been reviewed
	// against any documents yet, so nothing here is trusted to be unrelated
	// to them.
	case models.KycStatusIncomplete, models.KycStatusRequiresInfo:
		return len(requirements) > 0 && len(steps(requirements)) == 0
	}
	// DocumentSteps, not the full pending-step list: personal, profile and
	// address are answered through the application endpoint, and a provider
	// naming only one of those still has nothing for *this* call to fix. See
	// the doc comment above for the case that exists for.
	return len(DocumentSteps(steps(requirements))) == 0
}

// skipCheck answers whether a provider should be passed over, and why, in the
// two vocabularies the answer is read in: a stable token for the timeline,
// which filters and counts it, and a sentence for the log, which names the
// provider and its status for whoever is reading. A nil skipCheck means every
// implemented provider receives the submission — which is what an application
// does, since there is nothing yet to have satisfied.
type skipCheck func(code string) (skipped bool, reasonCode, reason string)

// fanOutProviders is the provider set a fan-out sends to:
// implementedProviders, logging each provider it passes over, narrowed to the
// ones that serve who. Every KYC fan-out entry point applies it before
// anything — an upstream customer, a ToS session, identity documents —
// reaches a provider at all.
//
// A provider left in the wrong mode, or enabled before its adapter ships, is
// an operator's doing, not anything the user caused or can act on, so it is
// logged loudly and passed over in silence: no outcome names it. A provider
// that does not serve who is passed over in silence too — it is no provider
// the user has any relationship with. Some enabled and none serving who is
// ErrJurisdictionNotServed; none enabled at all stays the caller's
// no_provider_available.
func (s *ProviderKycService) fanOutProviders(ctx context.Context, who *ramp.Person) ([]ramp.ProviderAdapter, error) {
	adapters, err := implementedProviders(s.config, s.factory, s.appEnv, func(code ramp.ProviderCode, reason error) {
		facades.Log().WithContext(ctx).Warningf("kyc: skipping %s in fan-out: %v", code, reason)
	})
	if err != nil {
		return nil, err
	}
	// Checked only when some provider is enabled: none at all stays the
	// caller's no_provider_available.
	if len(adapters) > 0 {
		if err := s.refuseClosedResidence(ctx, who); err != nil {
			return nil, err
		}
	}
	served, err := serving(adapters, who, pixRailLister(s.config))
	if err != nil {
		return nil, err
	}
	if len(adapters) > 0 && len(served) == 0 {
		return nil, ErrJurisdictionNotServed
	}
	return served, nil
}

// submitToEnabled runs submit once per provider fanOutProviders answered and
// applies the fan-out's decision rule: accepted when at least one provider
// took it, refused with the first provider's own error when none did.
//
// SubmitApplicationToEnabled and SubmitDocumentsToEnabled are both this loop
// with a different single-provider call closed over — the decision logic
// (when to skip, when the whole fan-out counts as accepted) lives here exactly
// once so the two cannot drift apart. Which providers to try is theirs: each
// reads who the submission is for its own way.
//
// userID is who sent the submission for customerID: a provider passed over is
// recorded as their call for it.
//
// It logs every per-provider failure and skip itself, rather than leaving
// that to the controller. mapError (shared.go) only logs an error it fails to
// recognise — by design, since a mapped sentinel is an expected outcome there.
// But by the time a fan-out outcome reaches the controller, it has already
// collapsed to a stable code: "this provider refused" and "this provider was
// never tried" both flatten to the same shape in SubmissionResource, and the
// controller has no way left to tell an operator which one happened. This
// loop still has both facts, so this is the last point that can log them.
func (s *ProviderKycService) submitToEnabled(
	ctx context.Context,
	userID, customerID uint,
	providers []ramp.ProviderAdapter,
	operation string,
	skip skipCheck,
	submit func(adapter ramp.ProviderAdapter) (*models.CustomerProviderKyc, error),
) ([]SubmissionOutcome, error) {
	outcomes := make([]SubmissionOutcome, 0, len(providers))
	for _, adapter := range providers {
		code := adapter.Code()
		if skip != nil {
			if skipped, reasonCode, reason := skip(code); skipped {
				facades.Log().WithContext(ctx).Infof("kyc: %s skipped in fan-out: %s", code, reason)
				// The line the timeline exists for: a provider that was never
				// asked leaves no call to read, and an operator cannot tell
				// that from one that was never attempted.
				s.record(rampsvc.Entry{
					UserID: userID, CustomerID: customerID, Provider: code, Operation: operation, SkipReason: reasonCode,
				})
				outcomes = append(outcomes, SubmissionOutcome{Provider: code, Skipped: true, SkipReason: reason})
				continue
			}
		}
		row, err := submit(adapter)
		if err != nil {
			facades.Log().WithContext(ctx).Errorf("kyc: fan-out submit to %s failed: %v", code, err)
		}
		outcomes = append(outcomes, SubmissionOutcome{Provider: code, Row: row, Err: err})
	}

	if len(outcomes) == 0 {
		return nil, ramp.ErrNoProviderAvailable
	}
	for _, outcome := range outcomes {
		if outcome.Accepted() {
			return outcomes, nil
		}
	}
	// Every provider was passed over: there was nothing to do, and nothing went
	// wrong. Returning an error here would invent one — and the user would see
	// it, for a resubmission that was simply unnecessary.
	if allSkipped(outcomes) {
		return outcomes, nil
	}
	// Nobody took it. Return the first refusal's own error rather than a
	// summary: a caller mapping this to a response needs a reason it can act
	// on, and the first in the declared order is the one the user would have
	// reached before the fan-out existed.
	return outcomes, firstError(outcomes)
}

func allSkipped(outcomes []SubmissionOutcome) bool {
	for _, outcome := range outcomes {
		if !outcome.Skipped {
			return false
		}
	}
	return true
}

// firstError is the first refusal in the declared provider order. Skipped
// entries carry no error and are stepped over.
func firstError(outcomes []SubmissionOutcome) error {
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			return outcome.Err
		}
	}
	return nil
}

// SubmitApplicationToEnabled submits one application to every enabled and
// implemented provider that serves the applicant, one job each.
//
// The client does not choose a provider — that is the whole point of a
// multi-provider stack, and the app cannot know which providers exist. What it
// sends is one form; which providers see it is this layer's answer: the ones
// whose jurisdiction policy serves the tax identity and residence the
// application declares (fanOutProviders). A passport never reaches a provider
// that serves only CPFs.
//
// Accepted when at least one provider took it. A provider refusing must not
// cost the user the one that worked, and the refusal is reported per provider
// so the client can retry that one through the {provider} route.
//
// It skips a provider that refused the user for good, and one that already
// holds a customer, is approved, under review, suspended or mid-submission,
// and owes no personal data, profile or address — the one state where an
// application satisfies nothing and re-opens a review (see
// applicationSkipCheck). Everyone else receives it: a provider with no
// customer is created from it, one with a customer is updated.
//
// The profile is canonicalised and retained here, once, before the
// per-provider loop — deliberately not inside submitApplication, which runs
// once per provider and would upsert the same declaration twice (three times,
// four…) in a single request. It is the user's own declaration, independent
// of what any one provider decides, so it is written before any of them are
// asked — but after the one check that is not about any provider either.
//
// That check is the email, and it gates retention because the profile carries
// one: the profile pre-fills the wizard a later provider opens, so it must
// never hold an address the account does not own. The refusal itself is still
// produced per provider, so the client reads one outcome per provider.
//
// A CPF or phone another account holds is refused before all of it, with no
// outcome at all (see checkIdentity), and so is an applicant no enabled
// provider serves (ErrJurisdictionNotServed): neither retains the declaration,
// since a refused one is the user's to correct, and retaining it would narrow
// the listings to its residence. The retained declaration keeps the tax id —
// with its type and issuing country, apart from the address — and the phone
// the application stands for, so omitting them never drops the digests that
// reserve them.
//
// The application is customer's, sent by user, whose account e-mail it must
// carry.
func (s *ProviderKycService) SubmitApplicationToEnabled(
	ctx context.Context, user *models.User, customer *models.Customer, input dto.ApplicationDTO,
) ([]SubmissionOutcome, error) {
	app, err := individualApplication(customer, input)
	if err != nil {
		return nil, err
	}
	customerID := customer.ID
	identity, err := s.checkIdentity(customerID, input.TaxID, taxIdentityOf(input), app.Profile.Phone)
	if err != nil {
		return nil, err
	}
	providers, err := s.fanOutProviders(ctx, applicant(input, identity))
	if err != nil {
		return nil, err
	}
	retained := retainedProfile(app, identity)
	if strings.EqualFold(strings.TrimSpace(app.Profile.Email), user.Email) {
		if err := s.retainProfile(customerID, retained); err != nil {
			return nil, err
		}
	}
	return s.submitToEnabled(ctx, user.ID, customerID, providers, rampsvc.OpSubmitApplication, s.applicationSkipCheck(customerID),
		func(adapter ramp.ProviderAdapter) (*models.CustomerProviderKyc, error) {
			return s.submitApplication(ctx, user, customerID, adapter.Code(), adapter, app, retained)
		})
}

// retainedProfile is the profile an application leaves behind: what it
// declares, with the tax id and phone it stands for (checkIdentity), which may
// be the retained ones when it omits them.
func retainedProfile(app ramp.Application, identity declaredIdentity) ramp.Profile {
	retained := app.Profile
	retained.TaxID, retained.Phone = identity.taxID, identity.phone
	retained.TaxIDType, retained.TaxIDCountry = identity.tax.Type, identity.tax.Country
	return retained
}

// retainProfile upserts the profile fields the design keeps so a provider
// added later asks only for the photographs again — never
// IdentifyingInformation, never LivenessCheckSelfies. The type enforces it:
// ramp.Profile is an alias for models.KycProfile (ports.go), which has no field
// that could carry either.
//
// It is also what reserves the CPF and phone: their digests are written with
// the profile. So a failure stops the submission, where it once only logged —
// sending the application anyway would put a CPF in front of the providers
// that nothing reserves for this account. The unique index refusing the CPF
// or phone is the same refusal checkIdentity gives: another account declared
// it in between.
func (s *ProviderKycService) retainProfile(customerID uint, profile ramp.Profile) error {
	if err := s.profileRepo.Upsert(customerID, profile); err != nil {
		return fmt.Errorf("kyc: retain profile for customer %d: %w", customerID, err)
	}
	return nil
}

// SubmitDocumentsToEnabled submits one set of documents to every enabled and
// implemented provider that serves the user, one job each. Same fan-out rule
// as SubmitApplicationToEnabled — see submitToEnabled — applied to
// SubmitDocuments instead. The documents declare no one, so who they are for
// is the retained profile the application left.
//
// Unlike the application, this one filters. The client collects the union of
// what every provider wants and sends it once; without a filter that union
// reaches providers that wanted none of it, and a provider that had already
// approved gets a fresh set of documents to review.
//
// The documents are customer's, sent by user.
func (s *ProviderKycService) SubmitDocumentsToEnabled(
	ctx context.Context, user *models.User, customerID uint, input dto.DocumentsDTO,
) ([]SubmissionOutcome, error) {
	docs, err := documentsFrom(input)
	if err != nil {
		return nil, err
	}
	who, err := s.persons.RetainedPerson(customerID)
	if err != nil {
		return nil, err
	}
	providers, err := s.fanOutProviders(ctx, who)
	if err != nil {
		return nil, err
	}
	return s.submitToEnabled(ctx, user.ID, customerID, providers, rampsvc.OpSubmitDocuments, s.documentSkipCheck(customerID),
		func(adapter ramp.ProviderAdapter) (*models.CustomerProviderKyc, error) {
			return s.submitDocuments(ctx, user, customerID, adapter.Code(), docs)
		})
}

// documentSkipCheck reads the user's providers once and answers from that
// snapshot.
//
// Once, not per provider: the loop asks about every enabled provider, and one
// list query answers all of them. It also keeps the check out of the way of
// SubmitDocuments, which loads its own row under a lock — reading the same row
// twice on the way to one decision is how the two would come to disagree about
// what it said.
//
// A provider with no row has never been submitted to, so it receives: an
// unknown state is not a reason to withhold. A repository error is treated the
// same way, deliberately — failing to read our own rows must not silently cost
// every provider its documents.
func (s *ProviderKycService) documentSkipCheck(customerID uint) skipCheck {
	snap := s.readSnapshot(customerID)
	if snap == nil {
		return nil
	}
	return func(code string) (bool, string, string) {
		row := snap.rows[code]
		if skipped, reasonCode, reason := skipTerminal(code, row); skipped {
			return skipped, reasonCode, reason
		}
		if !snap.holds[code] {
			// Nothing upstream to attach them to. Only an application in
			// flight — the same pass of the wizard, sent first — is about to
			// create one; the documents queue behind it. Anything else would
			// be staged, queued and dropped by the worker, leaving the row
			// `incomplete` with nothing on it.
			if row != nil && effectiveStatus(row, time.Now()) == models.KycStatusSubmitting {
				return false, "", ""
			}
			// A held application is what the documents of a provider whose
			// customer is born whole complete: they create the customer.
			if row != nil && row.ApplicationDeferredAt != nil && s.createsAtomically(code) {
				return false, "", ""
			}
			return true, "application_required", fmt.Sprintf("no customer at %s and no application in flight", code)
		}
		if row == nil {
			return false, "", ""
		}
		skip := ShouldSkipRedistribution
		if row.Business {
			skip = ShouldSkipBusinessRedistribution
		}
		if skip(effectiveStatus(row, time.Now()), OutstandingFor(row)) {
			return true, "nothing_owed", fmt.Sprintf("nothing owed at %s (status %s)", code, row.KycStatus)
		}
		return false, "", ""
	}
}

// createsAtomically reports whether provider creates its customer whole, from
// the application and the documents together (ramp.AtomicKycCreator).
func (s *ProviderKycService) createsAtomically(provider string) bool {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return false
	}
	_, ok := adapter.(ramp.AtomicKycCreator)
	return ok
}

// applicationSkipCheck passes over a provider an application cannot help:
// one that refused the user for good, or one holding a customer that is
// approved, under review, suspended, mid-submission or rejected for reasons
// the user can fix, and owing none of the steps an application carries
// (personal, profile, address). Any other provider with no customer receives
// it — that is how it gets one.
func (s *ProviderKycService) applicationSkipCheck(customerID uint) skipCheck {
	snap := s.readSnapshot(customerID)
	if snap == nil {
		return nil
	}
	return func(code string) (bool, string, string) {
		row := snap.rows[code]
		if skipped, reasonCode, reason := skipTerminal(code, row); skipped {
			return skipped, reasonCode, reason
		}
		if !snap.holds[code] || row == nil {
			return false, "", ""
		}
		// A row stuck on submitting has nothing in flight (stuckSubmission):
		// it is read as released, and receives. A rejection the user can fix
		// receives the steps it asked for, and the application only when
		// those include what it carries: a selfie that did not match is not
		// answered by restating the personal data. A rejection no adapter
		// classified keeps receiving it, as before the categories.
		switch effectiveStatus(row, time.Now()) {
		case models.KycStatusApproved, models.KycStatusUnderReview,
			models.KycStatusPaused, models.KycStatusSubmitting:
		case models.KycStatusRejected:
			if len(row.DecodedRejectionCategories()) == 0 {
				return false, "", ""
			}
		default:
			return false, "", ""
		}
		if owesApplicationData(PendingStepsFor(row)) {
			return false, "", ""
		}
		return true, "nothing_owed", fmt.Sprintf("nothing an application carries is owed at %s (status %s)", code, row.KycStatus)
	}
}

// skipTerminal passes over a provider that refused the user for good, ahead of
// every other rule: it never receives this user's data again, whether or not
// a customer stands behind the refusal (an Ozav create refused over the tax
// id has none, and would otherwise be sent the whole application again).
func skipTerminal(code string, row *models.CustomerProviderKyc) (bool, string, string) {
	if row == nil || !Terminal(row) {
		return false, "", ""
	}
	return true, "terminal", fmt.Sprintf("%s refused this user for good", code)
}

// snapshot is the user's providers as a fan-out filter reads them: each row,
// marked, and who holds a customer.
type snapshot struct {
	rows  map[string]*models.CustomerProviderKyc
	holds map[string]bool
}

// readSnapshot reads the user's providers once and answers from that for every
// provider in the loop — one query each, not one per provider, and kept out of
// the way of the submit call, which loads its own row under a lock.
//
// A read failure answers nil: every provider receives. Failing to read our own
// rows must not silently cost every provider the user's submission.
func (s *ProviderKycService) readSnapshot(customerID uint) *snapshot {
	rows, err := s.kycRepo.ListByCustomer(customerID)
	if err != nil {
		facades.Log().Errorf("kyc: reading providers for the fan-out filter: %v", err)
		return nil
	}
	links, err := s.integRepo.ListByCustomer(customerID)
	if err != nil {
		facades.Log().Errorf("kyc: reading integrations for the fan-out filter: %v", err)
		return nil
	}
	MarkNeedsApplication(rows, links, s.factory)
	snap := &snapshot{rows: make(map[string]*models.CustomerProviderKyc, len(rows)), holds: externalCustomerHolders(links)}
	for i := range rows {
		snap.rows[rows[i].Provider] = &rows[i]
	}
	return snap
}

// owesApplicationData reports whether steps include one only the application
// endpoint carries: a person's data, or a company's and its people's.
func owesApplicationData(steps []string) bool {
	for _, step := range steps {
		switch step {
		case stepPersonal, stepProfile, stepAddress,
			models.KybStepCompany, models.KybStepCompanyAddress, models.KybStepActivity, models.KybStepOwners:
			return true
		}
	}
	return false
}

// TosAcceptanceMode is how a provider's terms get accepted, and therefore how
// the client has to present them.
//
// It is derived from the adapter — a provider that implements ramp.TosAttester
// attests, everyone else hosts — rather than configured, because it is a fact
// about the provider's API and not a choice anyone makes.
const (
	// TosModeHosted: the provider serves the page, watches the user sign it
	// there, and hands back an agreement id. The client opens it in a WebView
	// and waits for that id.
	TosModeHosted = "hosted"
	// TosModeAttested: the provider hands over a document and takes XIP's word
	// that the user accepted it. The client presents the terms itself, with a
	// checkbox, and reports the acceptance — there is nothing to wait for,
	// because the page says nothing back.
	TosModeAttested = "attested"
)

// TosSessionView is one provider's terms page, ready for the client to open.
type TosSessionView struct {
	Provider     string
	URL          string
	AllowedHosts []string
	Mode         string
}

// tosApplicantFor is who the terms are for: the person input describes, or —
// for a business, which accepts them in its own name before its first
// application — the company its draft declared, whatever person input
// describes (contract §8).
func (s *ProviderKycService) tosApplicantFor(customerID uint, input dto.StartTosDTO) (*ramp.Person, error) {
	business, err := s.businessCustomer(customerID)
	if err != nil {
		return nil, fmt.Errorf("kyc: terms: %w", err)
	}
	if !business {
		return tosApplicant(input), nil
	}
	return s.persons.RetainedPerson(customerID)
}

// StartTosForEnabled opens a terms session at every enabled provider that
// serves the person the request declares, has terms and has not had them
// accepted yet.
//
// The client must not name a provider — it cannot know which ones exist — and
// with more than one having terms it cannot ask for them one at a time either.
// So it asks once and gets back the pending set, in provider order.
//
// A provider with no terms contributes nothing rather than an error:
// ErrTosNotRequired is the sentinel StartTos already returns for it, and it
// means "nothing to do here", which is a skip. An empty list is therefore a
// valid answer and the normal one for a stack whose providers have no terms.
// input.Modes is what the client can render. An empty one means a client
// that predates the question, and is read as hosted-only — the shape every
// build in the store today understands.
//
// input's Country, TaxIDType and TaxIDCountry are who the terms are for: the
// terms come before the application, so nothing retained says it yet. A
// passport is offered no terms of a provider that serves only CPFs, and a
// residence nobody serves is ErrJurisdictionNotServed. A request naming no
// residence — every build before them — is offered every provider's terms.
func (s *ProviderKycService) StartTosForEnabled(
	ctx context.Context, customerID uint, input dto.StartTosDTO,
) ([]TosSessionView, error) {
	// fanOutProviders, not every enabled provider: startTos below calls
	// adapter.CreateTosSession, a real upstream request regardless of
	// provider (unlike s.linker.Start just above it, which both Bridge's and
	// Ozav's own StartIntegration defer to SubmitApplication for a first-time
	// link) — the same class of side effect the fan-out's mode check exists to
	// keep away from a provider the environment disagrees with, or one that
	// does not serve the user. See fanOutProviders' doc.
	who, err := s.tosApplicantFor(customerID, input)
	if err != nil {
		return nil, err
	}
	providers, err := s.fanOutProviders(ctx, who)
	if err != nil {
		return nil, err
	}

	// One read for the whole loop rather than one per provider: the rows say
	// whose terms are signed and who refused the user for good.
	//
	// A read failure answers "not signed" and "not terminal" for everyone,
	// which is the same fail-open the per-provider check had: offering terms
	// again costs a page the user can close, withholding them costs the wizard
	// its step — and startTos still refuses a terminal provider on its own.
	var rows map[string]*models.CustomerProviderKyc
	if snap := s.readSnapshot(customerID); snap != nil {
		rows = snap.rows
	}

	sessions := make([]TosSessionView, 0, len(providers))
	var lastErr error
	attempted := 0
	for _, adapter := range providers {
		code := adapter.Code()
		// Asked before the provider is, not after. Checking the signature on
		// the row that startTos returns means every call re-created a session
		// upstream — a real POST, a fresh URL written over the stored one, and
		// a lock taken — for a value discarded on the next line.
		if row := rows[code]; row != nil && row.TosStatus == models.TosStatusApproved {
			continue
		}
		// A provider done with the user is offered no terms: nothing it could
		// be sent afterwards would reach it.
		if skipped, _, _ := skipTerminal(code, rows[code]); skipped {
			continue
		}
		mode := tosModeFor(adapter)
		// A client is never handed terms in a shape it cannot finish. The
		// build in the store draws every session as a WebView button waiting
		// for the page to post an agreement id back; an attesting provider's
		// page posts nothing, so that row would strand the user on the terms
		// step — and the step gates the whole wizard, so it would take every
		// other provider's KYC down with it.
		//
		// Skipping is the honest answer rather than an error: the provider is
		// simply not available to this client yet, exactly as one whose terms
		// are already signed is not.
		if !clientRenders(input.Modes, mode) {
			continue
		}
		attempted++
		row, session, err := s.startTos(ctx, customerID, code, input.RedirectURI)
		if errors.Is(err, ramp.ErrTosNotRequired) {
			continue
		}
		if err != nil {
			// One provider being unreachable must not cost the user the terms
			// they can accept right now. The client retries what is missing.
			facades.Log().WithContext(ctx).Errorf("kyc: tos session at %s: %v", code, err)
			lastErr = err
			continue
		}
		if row.TosStatus == models.TosStatusApproved {
			continue
		}
		sessions = append(sessions, TosSessionView{
			Provider:     code,
			URL:          session.URL,
			AllowedHosts: session.AllowedHosts,
			Mode:         mode,
		})
	}
	// An empty list is a valid answer — most stacks have no terms — but it
	// cannot also be how a total outage reads. The client skips the terms step
	// on an empty list and then fails on tos_required two steps later, which
	// tells the user nothing about what actually broke.
	if len(sessions) == 0 && attempted > 0 && lastErr != nil {
		return nil, lastErr
	}
	return sessions, nil
}

// tosModeFor reports how this provider's terms are accepted.
func tosModeFor(adapter ramp.ProviderAdapter) string {
	if _, attests := adapter.(ramp.TosAttester); attests {
		return TosModeAttested
	}
	return TosModeHosted
}

// clientRenders reports whether the client declared it can present this mode.
// An empty declaration is a client written before there was more than one
// shape, which is hosted.
func clientRenders(modes []string, mode string) bool {
	if len(modes) == 0 {
		return mode == TosModeHosted
	}
	for _, declared := range modes {
		if declared == mode {
			return true
		}
	}
	return false
}
