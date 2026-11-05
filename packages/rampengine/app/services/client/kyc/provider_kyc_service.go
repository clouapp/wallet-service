package kyc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	pushsvc "github.com/macrowallets/waas/packages/rampengine/app/services/push"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// ErrSubmissionInFlight rejects a second submission while one is queued. Each
// submission derives its own idempotency key, so letting two through would
// create two customers upstream for the same person.
var ErrSubmissionInFlight = errors.New("kyc: submission already in flight")

// ErrApplicationRequired refuses documents for a provider whose customer is
// born whole (ramp.AtomicKycCreator) when no application is held for them to
// complete: the documents alone cannot create the customer.
var ErrApplicationRequired = errors.New("kyc: an application is required before these documents")

// ErrSubmissionHeld refuses a submission, or releases the row of one, while
// the person's one atomic customer create is held for an operator: the
// automation will not send it, and nothing else may create a second customer
// until the operator decides what the first attempt did. No retry helps; the
// app tells the user support is looking at it.
var ErrSubmissionHeld = errors.New("kyc: the customer create is held for review")

// ErrVirtualAccountActive and ErrTransactionOpen refuse a retry with a new
// customer (retryWithNewCustomer) while money can still move at the provider
// on the customer being left: a standing account active or being
// provisioned, or an order not yet terminal. They are the refusals an unlink
// answers, under the same codes; the retry goes through once the money has
// settled.
var (
	ErrVirtualAccountActive = repositories.ErrVirtualAccountOpen
	ErrTransactionOpen      = repositories.ErrTransactionOpen
)

// ErrEmailMismatch rejects an application whose email does not match the
// authenticated user. The client cannot assert identity email — it comes from
// the account the bearer token belongs to.
var ErrEmailMismatch = errors.New("kyc: email mismatch")

// Linker ensures the customer is linked to a provider before a KYC call needs
// a customer reference. It is the integration service's Start, named as an
// interface so this package does not depend on that one's construction.
type Linker interface {
	Start(ctx context.Context, customerID uint, provider string) (*models.CustomerProviderIntegration, error)
}

// Dispatcher hands a staged submission to a worker. customerID and provider ride
// along so the worker can find the row it has to release even when the staged
// payload — which holds the same two fields — is already gone.
type Dispatcher interface {
	Dispatch(submissionID string, customerID uint, provider string) error
	// DispatchAfter re-queues a submission that cannot make progress yet. It
	// exists so the worker never waits on another job: with one worker, a job
	// that sleeps until a second job runs is a job sleeping until a job queued
	// behind it runs.
	DispatchAfter(submissionID string, customerID uint, provider string, delay time.Duration) error
	// RunsInline reports whether Dispatch runs the job before it returns, as
	// the sync driver does: the worker has then already written the row, and
	// there is no later for a re-queue to wait for.
	RunsInline() bool
}

// Recorder is the timeline this service writes to.
//
// An interface rather than the concrete service for one reason: the recorder
// lives in app/services/ramp, which is where the webhook processor and the
// reconciler already write, and taking the concrete type here would make this
// package depend on that one for a call it only ever makes one way.
type Recorder interface {
	Record(entry rampsvc.Entry)
}

// RequirementsView is what the customer still owes the provider. It exists so that
// ramp.KycRequirements.Raw — the provider's snapshot, which restates submitted
// customer fields — has no path to a client response.
type RequirementsView struct {
	Provider     string
	Status       string
	Missing      []string
	PendingSteps []string
	Endorsements []models.Endorsement
	// NeedsApplication: the provider holds no customer and owes the whole
	// application; PendingSteps lists every step of it.
	NeedsApplication bool
}

// ProviderKycService drives the compliance lifecycle at any provider.
type ProviderKycService struct {
	factory     *ramp.Factory
	config      *ramp.Config
	linker      Linker
	kycRepo     repositories.CustomerProviderKycRepository
	profileRepo repositories.CustomerIndividualProfileRepository
	persons     ramp.Persons
	integRepo   repositories.CustomerProviderIntegrationRepository
	members     repositories.CustomerMemberRepository
	staging     Staging
	dispatcher  Dispatcher
	locker      locking.Locker
	// audience tells every user who acts for a customer of its KYC changes.
	audience   broadcast.Audience
	notifier   pushsvc.Notifier
	appEnv     string
	timeline   Recorder
	features   contracts.Feature
	businesses repositories.CustomerBusinessProfileRepository
	customers  repositories.CustomerRepository
	users      repositories.UserRepository
	activator  BusinessActivator
	intents    repositories.ProviderKycSubmissionIntentRepository
	// intentRetention is how long a held intent keeps its material.
	intentRetention time.Duration
}

// BusinessActivator reconciles a business draft's status with its verdicts:
// approved at a provider, no other business holding its document, it is
// activated, and its approval told then (customers.KycVerdicts).
type BusinessActivator interface {
	ActivateIfApproved(customerID uint) (bool, error)
}

// Deps is what ProviderKycService is built from.
type Deps struct {
	Factory  *ramp.Factory
	Config   *ramp.Config
	Linker   Linker
	Kyc      repositories.CustomerProviderKycRepository
	Profiles repositories.CustomerIndividualProfileRepository
	// Persons reads who a customer declared itself to be, for the providers
	// that serve it.
	Persons      ramp.Persons
	Integrations repositories.CustomerProviderIntegrationRepository
	// Members is who acts for each customer, which is who hears of its
	// changes on the live stream.
	Members     repositories.CustomerMemberRepository
	Staging     Staging
	Dispatcher  Dispatcher
	Locker      locking.Locker
	Broadcaster broadcast.Broadcaster
	Notifier    pushsvc.Notifier
	AppEnv      string
	// Timeline records what was asked of each provider, and what was decided
	// against asking. Optional: a nil one records nothing.
	Timeline Recorder
	// Features holds the residences a flag keeps shut (kycflags.ResidenceFlag).
	// Optional: a nil one keeps every held residence shut.
	Features contracts.Feature
	// Businesses keeps a business's declaration: the company and its people.
	// Customers says which business holds a document past draft, and Users
	// the members' account e-mails, which the company's must not be. The
	// three serve a business's submissions alone.
	Businesses repositories.CustomerBusinessProfileRepository
	Customers  repositories.CustomerRepository
	Users      repositories.UserRepository
	// Activator reconciles an approved business draft on a read (Overview,
	// Refresh). Optional: without it a draft waits for the next verdict.
	Activator BusinessActivator
	// Intents holds the durable record of each atomic customer create
	// (ramp.AtomicKycCreator). Only those providers use it.
	Intents repositories.ProviderKycSubmissionIntentRepository
	// IntentMaterialRetention is how long the material of an intent held for
	// an operator is kept (kyc.intent_material_retention_hours, through
	// IntentMaterialRetention). Zero is the default.
	IntentMaterialRetention time.Duration
}

func NewProviderKycService(d Deps) *ProviderKycService {
	return &ProviderKycService{
		factory: d.Factory, config: d.Config, linker: d.Linker,
		kycRepo: d.Kyc, profileRepo: d.Profiles, persons: d.Persons, integRepo: d.Integrations, members: d.Members,
		staging: d.Staging, dispatcher: d.Dispatcher, locker: d.Locker, audience: broadcast.NewAudience(d.Broadcaster, d.Members),
		notifier: d.Notifier, appEnv: d.AppEnv, timeline: d.Timeline, features: d.Features,
		businesses: d.Businesses, customers: d.Customers, users: d.Users, activator: d.Activator,
		intents: d.Intents, intentRetention: orRetention(d.IntentMaterialRetention),
	}
}

// record writes one timeline entry, tolerating a service wired without one.
//
// The nil check is here rather than at each call site because a missed one is
// a hole in exactly the record built to show holes.
func (s *ProviderKycService) record(entry rampsvc.Entry) {
	if s.timeline == nil {
		return
	}
	s.timeline.Record(entry)
}

// StartTos issues the provider's terms-of-service page and records it. A
// provider that does not serve the person the retained profile declares is
// refused before it is asked, and so is one whose residence a flag holds
// shut (ErrJurisdictionNotServed).
func (s *ProviderKycService) StartTos(ctx context.Context, customerID uint, provider, redirectURI string) (*models.CustomerProviderKyc, error) {
	if err := s.refuseDisabled(provider); err != nil {
		return nil, err
	}
	if err := s.refuseUnservedRetained(ctx, customerID, provider); err != nil {
		return nil, err
	}
	row, _, err := s.startTos(ctx, customerID, provider, redirectURI)
	return row, err
}

// refuseDisabled refuses a provider an operator has not switched on, or left
// in a mode APP_ENV does not run, before the per-provider routes ask it
// anything: the same providers the fan-outs pass over (implementedProviders),
// answered with the fan-out's own no_provider_available. An unknown code is
// the registry's refusal, as before.
func (s *ProviderKycService) refuseDisabled(provider string) error {
	if _, err := s.factory.Get(provider); err != nil {
		return err
	}
	if s.config == nil {
		return nil
	}
	state, err := s.config.Provider(provider)
	if err != nil {
		return fmt.Errorf("kyc: read %s state: %w", provider, err)
	}
	if !state.IsUsable() || ramp.ValidateMode(state.Mode, s.appEnv) != nil {
		return fmt.Errorf("kyc: provider %q is switched off: %w", provider, ramp.ErrNoProviderAvailable)
	}
	return nil
}

// tosSessionFromRow rebuilds the session object from what was stored, for the
// pass that reuses one instead of asking the provider again.
//
// It carries no AllowedHosts, and does not need to: those bound the WebView a
// hosted terms page is rendered in, and only a provider that issues a token
// before acceptance reaches this path — one whose terms are presented by the
// app itself, with no page of the provider's to sandbox.
func tosSessionFromRow(row *models.CustomerProviderKyc) *ramp.TosSession {
	session := &ramp.TosSession{}
	if row.TosLinkURL != nil {
		session.URL = *row.TosLinkURL
	}
	if row.TosSessionID != nil {
		session.SessionID = *row.TosSessionID
	}
	if row.TosExpiresAt != nil {
		session.ExpiresAt = *row.TosExpiresAt
	}
	return session
}

// startTos is StartTos keeping the provider's session object.
//
// The session carries the hosts its page may load from, which the single
// -provider route has no use for and the fan-out does: the client needs an
// allowlist to enforce around the WebView it renders the page in.
func (s *ProviderKycService) startTos(ctx context.Context, customerID uint, provider, redirectURI string) (*models.CustomerProviderKyc, *ramp.TosSession, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, nil, err
	}
	// A stub provider must answer "not implemented", not "no terms to sign" —
	// the latter is technically true but sends the client on to take a 503 on
	// the very next call instead of learning the real reason now.
	if !s.factory.IsImplemented(provider) {
		return nil, nil, fmt.Errorf("provider %q: %w", provider, ramp.ErrProviderNotImplemented)
	}
	// Asked of a provider with no terms, this is not a failure — there is
	// nothing to sign. Refusing here, with a sentinel the caller can branch
	// on, is honest; issuing a fake approved status would put a signature we
	// never collected on the record.
	//
	// It comes before linker.Start on purpose: starting an integration to then
	// announce there is no step to run would be a round trip bought for
	// nothing, and RequiresTos is a constant of the provider, not of the user.
	if !adapter.RequiresTos() {
		return nil, nil, ramp.ErrTosNotRequired
	}

	// A session the row already holds is presented again rather than replaced,
	// and this is not an optimization. A provider that issues its token once —
	// Ozav keeps only the hash — answers a second request with a session
	// carrying no token at all, so minting on every pass is how the user ends
	// up holding a session nothing can be provisioned against. The stored row
	// is the deduplication; the adapters send no idempotency key for it.
	//
	// It runs before linker.Start for the same reason RequiresTos does: an
	// integration opened to then discover there was nothing to do is a round
	// trip bought for nothing.
	//
	// A row a provider that creates its customer whole has rejected is the
	// exception: its session opened the terms of the customer it refused, and
	// the retry needs a new customer with terms of its own (retryWithNewCustomer).
	existing, err := s.kycRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		existing = nil
	}
	retry := existing != nil && retriesWithNewCustomer(adapter, existing)
	if existing != nil && !retry && existing.HoldsLiveTosSession(time.Now()) {
		return existing, tosSessionFromRow(existing), nil
	}

	link, err := s.linker.Start(ctx, customerID, provider)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil {
		if err := s.guardTerminal(existing, hasExternalCustomer(link)); err != nil {
			return nil, nil, err
		}
	}
	if retry {
		if link, err = s.retryWithNewCustomer(ctx, customerID, provider, adapter, link); err != nil {
			return nil, nil, err
		}
	}

	// Outside the lock: WithLock holds a Postgres transaction open for its whole
	// callback, and an upstream request inside it keeps a connection `idle in
	// transaction` for the provider's full latency. Two concurrent calls are
	// safe to let through — the idempotency key is derived from (user, provider,
	// redirect), so the provider returns the same terms page to both while still
	// allowing a new session when the redirect URI changes.
	idempotencyKey := derivedTosKey(customerID, provider, redirectURI, time.Now())
	// Local dev often rotates Bridge sandbox keys; a poisoned idempotency key then
	// 422s forever on the same redirect. Fresh keys avoid that without affecting prod.
	//
	// A retry with a new customer takes a fresh key too: the day's derived key
	// may be the one that opened the refused customer's terms, and a provider
	// that keys the session by it would hand those back. Decided from the row
	// as read before the lock, so a call that found the reset already done by
	// a concurrent one does not fetch the old session either.
	if s.appEnv == "local" || retry {
		idempotencyKey = uuid.NewString()
	}
	session, err := adapter.CreateTosSession(ctx, ramp.NewCustomerRef(customerID, link.ExternalCustomerID),
		ramp.TosOpts{RedirectURI: redirectURI, IdempotencyKey: idempotencyKey})
	if err != nil {
		return nil, nil, fmt.Errorf("kyc: start tos at %s: %w", provider, err)
	}

	var result *models.CustomerProviderKyc
	// The lock still covers the read-modify-write of the row, which is what two
	// concurrent calls would otherwise race on.
	// The terms lock is not the submit lock: a mark or a worker's answer can
	// land between this read and its write, so the write is on the status
	// read, and read again when it moved (untilSettled).
	err = s.locker.WithLock(ctx, lockKey("tos", customerID, provider), func(context.Context) error {
		return untilSettled(func() error {
			var err error
			result, err = s.recordTosSession(customerID, provider, adapter, session)
			return err
		})
	})
	if err != nil {
		return nil, nil, err
	}
	return result, session, nil
}

// retriesWithNewCustomer reports whether row is a rejection a provider whose
// customer is born whole (ramp.AtomicKycCreator) answers with a brand-new
// customer: that customer's KYC data cannot be changed, and each accepted
// terms id belongs to one customer only. A terminal refusal is never retried;
// guardTerminal refuses it before anything here runs.
func retriesWithNewCustomer(adapter ramp.ProviderAdapter, row *models.CustomerProviderKyc) bool {
	_, atomic := adapter.(ramp.AtomicKycCreator)
	return atomic && row.KycStatus == models.KycStatusRejected
}

// retryWithNewCustomer turns the rejected row of a provider whose customer is
// born whole back into a fresh start, under the submit lock: the customer is
// detached from the link (and the off-ramp destinations registered against
// it dropped), the terms and the verdict cleared, and the application held
// for the old customer forgotten. Detach and row land in one transaction
// (DetachCustomer), and the row being rejected is the trigger whatever the
// link says, so a retry after anything that half-landed finishes the reset.
//
// Nothing is detached while a create is still active for the person — it may
// be linking a customer right now — or held for an operator. A row already
// reset by a concurrent call is left as it is. It answers the link as it now
// stands.
func (s *ProviderKycService) retryWithNewCustomer(
	ctx context.Context, customerID uint, provider string, adapter ramp.ProviderAdapter, link *models.CustomerProviderIntegration,
) (*models.CustomerProviderIntegration, error) {
	var reset *models.CustomerProviderKyc
	err := s.locker.WithLock(ctx, lockKey("submit", customerID, provider), func(context.Context) error {
		return untilSettled(func() error {
			reset = nil
			row, err := s.kycRepo.FindByCustomerAndProvider(customerID, provider)
			if err != nil {
				return fmt.Errorf("kyc: lookup: %w", err)
			}
			if row == nil || !retriesWithNewCustomer(adapter, row) {
				return nil
			}
			current, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
			if err != nil {
				return fmt.Errorf("kyc: lookup integration: %w", err)
			}
			if current == nil {
				return ramp.ErrIntegrationRequired
			}
			if err := s.guardTerminal(row, hasExternalCustomer(current)); err != nil {
				return err
			}
			if err := s.refuseActiveIntent(customerID, provider); err != nil {
				return err
			}
			// The detach lands only on the row as read here — its status and
			// its rejection categories — so a webhook that turned the refusal
			// terminal in between is not detached over.
			read := *row
			row.ResetTos()
			clearVerdict(row, adapter.PreferredKycMode().String())
			row.ApplicationDeferredAt = nil
			written, err := s.integRepo.DetachCustomer(current, row, &read)
			switch {
			case errors.Is(err, repositories.ErrActiveKycIntentExists):
				return ErrSubmissionInFlight
			case errors.Is(err, repositories.ErrIntegrationGone):
				return ramp.ErrIntegrationRequired
			case errors.Is(err, ErrVirtualAccountActive), errors.Is(err, ErrTransactionOpen):
				// The user's own state, not a fault: it answers 409 and clears
				// once the money settles.
				facades.Log().Infof("kyc: customer %d at %s: retry with a new customer waits: %v", customerID, provider, err)
				return fmt.Errorf("kyc: retry with a new customer: %w", err)
			case errors.Is(err, repositories.ErrLinkConflict):
				// The link moved since it was read: read both again.
				return errRowMoved
			case err != nil:
				return fmt.Errorf("kyc: detach the rejected customer: %w", err)
			case !written:
				return errRowMoved
			}
			link, reset = current, row
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if reset == nil {
		// Reset by a concurrent call: the link read before the lock may still
		// name the customer that call detached.
		current, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
		if err != nil {
			return nil, fmt.Errorf("kyc: lookup integration: %w", err)
		}
		if current == nil {
			return nil, ramp.ErrIntegrationRequired
		}
		return current, nil
	}
	facades.Log().Infof("kyc: customer %d at %s: rejected, reset for a retry with a new customer", customerID, provider)
	markApplication(reset, false, s.factory)
	s.publish(reset, "")
	return link, nil
}

// refuseActiveIntent answers why no customer may be detached while the
// person's one atomic create is still active: ErrSubmissionHeld when an
// operator holds it, ErrSubmissionInFlight otherwise.
func (s *ProviderKycService) refuseActiveIntent(customerID uint, provider string) error {
	if s.intents == nil {
		return nil
	}
	intent, _, err := s.intents.FindActive(customerID, provider)
	if intent != nil && intent.State == models.KycIntentManualReview {
		return ErrSubmissionHeld
	}
	if intent != nil {
		return ErrSubmissionInFlight
	}
	if err != nil {
		return fmt.Errorf("kyc: read the active intent: %w", err)
	}
	return nil
}

// recordTosSession writes a terms session onto the row, once per attempt.
func (s *ProviderKycService) recordTosSession(
	customerID uint, provider string, adapter ramp.ProviderAdapter, session *ramp.TosSession,
) (*models.CustomerProviderKyc, error) {
	row, isNew, err := s.loadRow(customerID, provider, adapter)
	if err != nil {
		return nil, err
	}

	row.TosLinkURL = &session.URL
	// The token and its deadline, for a provider that issues one before it
	// is accepted. Deliberately not written to SignedAgreementID: the
	// submission gate reads that field's presence as proof the user
	// signed, and nothing has been accepted at this point.
	if session.SessionID != "" {
		sessionID := session.SessionID
		row.TosSessionID = &sessionID
		row.TosExpiresAt = nil
		if !session.ExpiresAt.IsZero() {
			expiresAt := session.ExpiresAt
			row.TosExpiresAt = &expiresAt
		}
	}
	if session.SignedAgreementID != "" {
		row.SignedAgreementID = &session.SignedAgreementID
		row.TosStatus = models.TosStatusApproved
	}
	if err := s.saveRowUnmoved(row, isNew); err != nil {
		return nil, err
	}
	return row, nil
}

// SubmitApplication accepts an identity submission for customer and queues
// it. Staged, not sent (see StagedSubmission): a worker sends it and reports
// back over the KYC event stream. It retains no profile, but it sends the CPF,
// so a CPF or phone another account holds is refused here as in the fan-out
// (checkIdentity), and so is an applicant the named provider does not serve
// (ErrJurisdictionNotServed). user is who sends it: the application must carry
// the e-mail of their account.
func (s *ProviderKycService) SubmitApplication(
	ctx context.Context, user *models.User, customer *models.Customer, provider string, input dto.ApplicationDTO,
) (*models.CustomerProviderKyc, error) {
	// The registry lookup first: an unknown provider is refused without
	// spending a query on it.
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	if err := s.refuseDisabled(provider); err != nil {
		return nil, err
	}
	app, err := individualApplication(customer, input)
	if err != nil {
		return nil, err
	}
	identity, err := s.checkIdentity(customer.ID, input.TaxID, taxIdentityOf(input), app.Profile.Phone)
	if err != nil {
		return nil, err
	}
	who := applicant(input, identity)
	if err := s.refuseClosedResidence(ctx, who); err != nil {
		return nil, err
	}
	if err := s.refuseUnserved(adapter, who); err != nil {
		return nil, err
	}
	return s.submitApplication(ctx, user, customer.ID, provider, adapter, app, retainedProfile(app, identity))
}

// individualApplication is the person's form as customer's application, of
// the customer's type. The person's form serves an individual only: a company
// applies with its own (KYB spec §7), so a business reaching it is refused
// before anything is checked, staged or sent — sent, it would reach a
// provider as a company described by a person's fields.
func individualApplication(customer *models.Customer, input dto.ApplicationDTO) (ramp.Application, error) {
	if customer.Type != models.CustomerTypeIndividual {
		return ramp.Application{}, fmt.Errorf("kyc: customer %d is a %s, which the person's form does not serve",
			customer.ID, customer.Type)
	}
	app := applicationFrom(input)
	app.Type = customer.Type
	return app, nil
}

// submitApplication is SubmitApplication once the input is canonical, so the
// fan-out canonicalises once rather than once per provider. retained is the
// profile the application leaves behind (retainedProfile); only a provider
// that defers the application writes it from here.
func (s *ProviderKycService) submitApplication(
	ctx context.Context, user *models.User, customerID uint, provider string, adapter ramp.ProviderAdapter,
	app ramp.Application, retained ramp.Profile,
) (*models.CustomerProviderKyc, error) {
	// Before linker.Start, so a refused application opens no integration. It
	// stays here, per provider, rather than moving up into the fan-out: the
	// client reads one outcome per provider, and a single early return would
	// empty that list.
	// A business application names no person of the account: its e-mail is
	// the company's (contract §5.4).
	if app.Business == nil && !strings.EqualFold(strings.TrimSpace(app.Profile.Email), user.Email) {
		return nil, ErrEmailMismatch
	}
	// Called for its effect, not its value: the worker resolves the customer
	// reference from the integration row, so that row has to exist before the
	// submission is queued.
	link, err := s.linker.Start(ctx, customerID, provider)
	if err != nil {
		return nil, err
	}

	var staged stagedSubmit
	// The refusal below travels out here instead of being returned from the
	// callback. WithLock runs it inside a transaction, so returning an error
	// is what rolls back — including the row that records why it was refused.
	var refused error
	err = s.locker.WithLock(ctx, lockKey("submit", customerID, provider), func(_ context.Context) error {
		return untilSettled(func() error {
			refused = nil
			return s.stageApplication(user.ID, customerID, provider, adapter, link, &app, retained, &staged, &refused)
		})
	})
	if err != nil {
		return nil, err
	}
	if refused != nil {
		return nil, refused
	}
	if staged.held() {
		return staged.row, nil
	}
	return s.dispatchStagedSubmission(staged)
}

// stageApplication is submitApplication's locked read-decide-write, run again
// by untilSettled when the row moves under it.
func (s *ProviderKycService) stageApplication(
	userID, customerID uint, provider string, adapter ramp.ProviderAdapter, link *models.CustomerProviderIntegration,
	app *ramp.Application, retained ramp.Profile, staged *stagedSubmit, refused *error,
) error {
	row, isNew, err := s.loadRow(customerID, provider, adapter)
	if err != nil {
		return err
	}
	// Not recorded: `submitting` says one is on its way to the provider, so
	// nothing has failed. Writing a failure here would replace a real
	// refusal with a duplicate tap.
	if err := s.guardInFlight(row, KindApplication, customerID, provider); err != nil {
		return err
	}
	if err := s.guardTerminal(row, hasExternalCustomer(link)); err != nil {
		return err
	}

	// The agreement is captured at the ToS step, so a client that already
	// signed need not carry it again. Failing here spares the provider a
	// rejection and keeps identity documents out of Redis for a submission
	// that could not succeed.
	//
	// A rejected row of a provider whose customer is born whole is retried
	// with a new customer, which starts at the terms (retryWithNewCustomer):
	// the agreement the row holds, or one the client repeats, was the refused
	// customer's, so the application is refused until the new terms run.
	//
	// A customer born whole is created with the agreement and the row reads
	// its terms as approved from then on (deferApplication), so the agreement
	// must be the one this side recorded an acceptance of (RecordTosAcceptances,
	// after a session StartTos opened) — not one the client names on the
	// application alone, which would mark consent nobody recorded.
	staleTerms := retriesWithNewCustomer(adapter, row)
	if app.SignedAgreementID == "" && row.SignedAgreementID != nil && !staleTerms {
		app.SignedAgreementID = *row.SignedAgreementID
	}
	atomic, atomicCreate := adapter.(ramp.AtomicKycCreator)
	atomicCreate = atomicCreate && !hasExternalCustomer(link) && app.Business == nil
	unrecordedTerms := atomicCreate && adapter.RequiresTos() &&
		(row.SignedAgreementID == nil || *row.SignedAgreementID != app.SignedAgreementID)
	if staleTerms || unrecordedTerms || app.SignedAgreementID == "" && adapter.RequiresTos() {
		// Recorded on the row, not only returned. This is the one refusal
		// that happens before anything is staged, so none of the release
		// paths that record a failure ever run — and the client does not
		// stop at the 409: it sends its documents next, the worker drops
		// them for want of a customer and releases the row to
		// `incomplete`. By the time the user looks at the screen the
		// response is long gone and the row said nothing about why, which
		// is a verification that reads as under way and can never move.
		row.RecordSubmissionFailure(
			failureCode(ramp.ErrTosRequired), failureDetail(ramp.ErrTosRequired))
		if err := s.saveRowUnmoved(row, isNew); err != nil {
			return err
		}
		*refused = ramp.ErrTosRequired
		return nil
	}
	// Persist the agreement id for the worker payload. ToS approval comes
	// from Bridge's customer response, not from the client's declaration.
	// Guarded so a provider without terms never writes a pointer to an
	// empty string — NULL is what "no agreement" means on this column.
	if app.SignedAgreementID != "" {
		row.SignedAgreementID = &app.SignedAgreementID
	}
	if atomicCreate {
		*staged, err = s.deferApplication(customerID, atomic, row, isNew, app, retained)
		return err
	}

	*staged, err = s.stageSubmission(row, isNew, StagedSubmission{
		CustomerID: customerID, UserID: userID, Provider: provider, Kind: KindApplication,
		Application: app, PreviousStatus: row.KycStatus,
	})
	return err
}

// deferApplication holds the application of a provider whose customer is born
// whole (ramp.AtomicKycCreator): it cannot be sent without the documents, so
// nothing is staged or queued. The adapter checks it now, so a field the
// create would refuse is a 422 here rather than a failed job after the
// documents; the row then waits at the state the adapter answers, naming the
// documents, and ApplicationDeferredAt says the application was given — without
// it, a row with no customer reads as owing it (markApplication).
//
// An intent still active — the create in flight, or held for an operator —
// refuses it as submission_in_flight or submission_held, as the documents are.
//
// The profile is retained here, under the lock, as the fan-out does: the
// create reads it back when the documents arrive, and its write is what
// reserves the CPF and phone. The fan-out has already written the same
// profile; the second upsert changes nothing.
//
// The terms read as approved: the provider has no terms session of its own to
// answer from, and the gate above admitted only the agreement whose acceptance
// this side recorded, which is what the create carries. The write lands only on the status the row was read with.
func (s *ProviderKycService) deferApplication(
	customerID uint, atomic ramp.AtomicKycCreator, row *models.CustomerProviderKyc, isNew bool,
	app *ramp.Application, retained ramp.Profile,
) (stagedSubmit, error) {
	state, err := atomic.ValidateDeferredApplication(*app)
	if err != nil {
		return stagedSubmit{}, err
	}
	// The person's one create is still active, made from the application held
	// before: holding another would change what a replay of it no longer can.
	if err := s.refuseActiveIntent(customerID, row.Provider); err != nil {
		return stagedSubmit{}, err
	}
	if err := s.retainProfile(customerID, retained); err != nil {
		return stagedSubmit{}, err
	}
	previous := row.KycStatus
	now := time.Now()
	row.KycStatus = state.Status
	row.TosStatus = models.TosStatusApproved
	row.SetRequirements(state.Requirements)
	if err := row.SetEndorsements(state.Endorsements); err != nil {
		return stagedSubmit{}, fmt.Errorf("kyc: encode endorsements: %w", err)
	}
	row.ApplicationDeferredAt = &now
	row.ClearSubmissionFailure()
	written, err := s.saveRowIf(row, isNew, previous)
	if err != nil {
		return stagedSubmit{}, err
	}
	if !written {
		return stagedSubmit{}, errRowMoved
	}
	return stagedSubmit{row: row}, nil
}

// SubmitDocuments queues additional documents for a customer that already
// exists. Same transport reasoning as SubmitApplication. A provider that does
// not serve the person the retained profile declares is refused before
// anything is staged, and so is one whose residence a flag holds shut
// (ErrJurisdictionNotServed). user is who sends them, for the provider
// timeline.
func (s *ProviderKycService) SubmitDocuments(
	ctx context.Context, user *models.User, customerID uint, provider string, input dto.DocumentsDTO,
) (*models.CustomerProviderKyc, error) {
	docs, err := documentsFrom(input)
	if err != nil {
		return nil, err
	}
	if err := s.refuseDisabled(provider); err != nil {
		return nil, err
	}
	if err := s.refuseUnservedRetained(ctx, customerID, provider); err != nil {
		return nil, err
	}
	return s.submitDocuments(ctx, user, customerID, provider, docs)
}

// submitDocuments is SubmitDocuments once the documents are built, so the
// fan-out refuses an incomplete body once rather than once per provider.
func (s *ProviderKycService) submitDocuments(
	ctx context.Context, user *models.User, customerID uint, provider string, docs ramp.Documents,
) (*models.CustomerProviderKyc, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	// A provider whose customer is born whole checks the documents and the
	// client IP now, before anything is linked or staged: a bad image is a
	// 422 here, not a failed job after a 202.
	atomic, isAtomic := adapter.(ramp.AtomicKycCreator)
	if isAtomic {
		if err := atomic.ValidateAtomicDocuments(docs); err != nil {
			return nil, err
		}
	}
	// Called for its effect, as in submitApplication. The customer behind it
	// may not exist yet, and that is not a refusal: in the wizard the documents
	// follow the application immediately, and the application worker is what
	// creates the customer. Refusing would move the wait to the client, which
	// would re-upload and re-stage the images on every poll; the worker waits
	// instead, next to the payload (see requeueForCustomer).
	link, err := s.linker.Start(ctx, customerID, provider)
	if err != nil {
		return nil, err
	}

	var staged stagedSubmit
	err = s.locker.WithLock(ctx, lockKey("submit", customerID, provider), func(_ context.Context) error {
		return untilSettled(func() error {
			row, isNew, err := s.loadRow(customerID, provider, adapter)
			if err != nil {
				return err
			}
			if err := s.guardInFlight(row, KindDocuments, customerID, provider); err != nil {
				return err
			}
			if err := s.guardTerminal(row, hasExternalCustomer(link)); err != nil {
				return err
			}
			kind := KindDocuments
			if isAtomic {
				if kind, err = s.atomicDocumentsKind(customerID, provider, link, row); err != nil {
					return err
				}
			}
			staged, err = s.stageSubmission(row, isNew, StagedSubmission{
				CustomerID: customerID, UserID: user.ID, Provider: provider, Kind: kind,
				Documents: &docs, PreviousStatus: row.KycStatus,
			})
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return s.dispatchStagedSubmission(staged)
}

// atomicDocumentsKind decides, under the submit lock, what documents sent to a
// provider whose customer is born whole become.
//
// The link is read again here rather than trusted from linker.Start: the
// worker of an earlier create links the customer outside this lock. With a
// customer already there, the documents are refused here
// (ErrDocumentsNotSupported, a 409): nothing can be added to it, and staging
// them would only queue a job to fail on it.
// Without one they create it — from the application held for them, so with
// none held they are refused; and one create at a time, so an intent still
// active is a submission in flight, whatever the row says.
func (s *ProviderKycService) atomicDocumentsKind(
	customerID uint, provider string, link *models.CustomerProviderIntegration, row *models.CustomerProviderKyc,
) (string, error) {
	current, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return "", fmt.Errorf("kyc: lookup integration: %w", err)
	}
	if current == nil {
		current = link
	}
	if hasExternalCustomer(current) {
		return "", fmt.Errorf("kyc: %s customer is born whole: %w", provider, ramp.ErrDocumentsNotSupported)
	}
	if row.ApplicationDeferredAt == nil {
		return "", ErrApplicationRequired
	}
	if s.intents == nil {
		return "", errors.New("kyc: no intent repository for an atomic provider")
	}
	intent, _, err := s.intents.FindActive(customerID, provider)
	if intent != nil && intent.State == models.KycIntentManualReview {
		return "", ErrSubmissionHeld
	}
	if intent != nil {
		return "", ErrSubmissionInFlight
	}
	if err != nil {
		return "", fmt.Errorf("kyc: read the active intent: %w", err)
	}
	return KindAtomicCustomer, nil
}

// HostedLink issues the provider's hosted verification page. It runs in the
// request: the payload is a name and a redirect, not photographs. It opens a
// customer and a verification at the provider, so one that does not serve the
// person the retained profile declares is refused before it is asked
// (ErrJurisdictionNotServed), or whose residence a flag holds shut — the
// operator's link included. The page is addressed to user's account e-mail.
func (s *ProviderKycService) HostedLink(
	ctx context.Context, user *models.User, customerID uint, provider string, input dto.HostedLinkDTO,
) (*models.CustomerProviderKyc, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	if err := s.refuseUnservedRetained(ctx, customerID, provider); err != nil {
		return nil, err
	}
	link, err := s.linker.Start(ctx, customerID, provider)
	if err != nil {
		return nil, err
	}
	row, isNew, err := s.loadRow(customerID, provider, adapter)
	if err != nil {
		return nil, err
	}
	if err := s.guardTerminal(row, hasExternalCustomer(link)); err != nil {
		return nil, err
	}

	opts := ramp.HostedLinkOpts{
		FullName:       input.FullName,
		Email:          user.Email,
		Endorsements:   input.Endorsements,
		RedirectURI:    input.RedirectURI,
		Reason:         input.Reason,
		IdempotencyKey: derivedKey("kyc-link", customerID, provider),
	}
	if row.Business {
		// A company's page is the provider's business flow, named by the
		// company's legal name unless the body names it (contract §5.1).
		opts.Type = models.CustomerTypeBusiness
		if opts.FullName == "" {
			if opts.FullName, err = s.legalNameOf(customerID); err != nil {
				return nil, err
			}
		}
	}
	result, err := adapter.CreateHostedKycLink(ctx, ramp.NewCustomerRef(customerID, link.ExternalCustomerID), opts)
	if err != nil {
		return nil, fmt.Errorf("kyc: hosted link at %s: %w", provider, err)
	}

	// The hosted link is the other way a customer is born, so the id it returns
	// is what every later call addresses. It is written onto the row as it is
	// when written: on the status it was read with, and read again when the
	// row moved during the provider call — the link fields decide nothing
	// about the status, so they belong on whatever the row now holds.
	err = untilSettled(func() error {
		row.HostedKycLinkID = &result.LinkID
		row.HostedKycURL = &result.URL
		if result.CollectionMode != "" {
			row.CollectionMode = result.CollectionMode.String()
		}
		written, err := s.linkAndSave(link, result.ExternalCustomerID, row, isNew, row.KycStatus)
		if err != nil || written {
			return err
		}
		if row, isNew, err = s.loadRow(customerID, provider, adapter); err != nil {
			return err
		}
		return errRowMoved
	})
	if err != nil {
		return nil, err
	}
	s.publish(row, "")
	return row, nil
}

// RequestEndorsements asks the provider for additional capabilities, then
// persists whatever state it answers with. Asking opens state at the
// provider, so one that does not serve the person the retained profile
// declares, or whose residence a flag holds shut, is refused before it is
// asked (ErrJurisdictionNotServed); Refresh,
// a read, is not.
func (s *ProviderKycService) RequestEndorsements(ctx context.Context, customerID uint, provider string, endorsements []string) (*models.CustomerProviderKyc, error) {
	if err := s.refuseUnservedRetained(ctx, customerID, provider); err != nil {
		return nil, err
	}
	return s.syncFromProvider(ctx, customerID, provider, "request endorsements", true,
		func(ctx context.Context, adapter ramp.ProviderAdapter, ref ramp.CustomerRef) (*ramp.KycState, error) {
			return adapter.RequestEndorsements(ctx, ref, endorsements)
		})
}

// Refresh reconciles the local KYC row with the provider.
func (s *ProviderKycService) Refresh(ctx context.Context, customerID uint, provider string) (*models.CustomerProviderKyc, error) {
	return s.syncFromProvider(ctx, customerID, provider, "refresh", false,
		func(ctx context.Context, adapter ramp.ProviderAdapter, ref ramp.CustomerRef) (*ramp.KycState, error) {
			return adapter.RefreshKyc(ctx, ref)
		})
}

// syncFromProvider asks the provider about a customer and keeps the answer.
// operation names the call in the error wrap. asks says the call asks the
// provider for something on the user's behalf rather than only reading, which
// a provider done with the user must not receive (guardTerminal).
func (s *ProviderKycService) syncFromProvider(
	ctx context.Context, customerID uint, provider, operation string, asks bool,
	fetch func(context.Context, ramp.ProviderAdapter, ramp.CustomerRef) (*ramp.KycState, error),
) (*models.CustomerProviderKyc, error) {
	adapter, ref, err := s.linkedCustomer(customerID, provider)
	if err != nil {
		return nil, err
	}
	row, isNew, err := s.loadRow(customerID, provider, adapter)
	if err != nil {
		return nil, err
	}
	if asks {
		if err := s.guardTerminal(row, ref.ExternalID != ""); err != nil {
			return nil, err
		}
	}

	previous, read := row.Posture(), row.KycStatus
	state, err := fetch(ctx, adapter, ref)
	if err != nil {
		return nil, fmt.Errorf("kyc: %s at %s: %w", operation, provider, err)
	}
	standing := *row
	applyKycState(row, state)
	keepDocumentRefusal(row, &standing)
	// On the status read before the call: a row that moved meanwhile (a
	// worker's answer, a webhook, an admin reset) holds something at least as
	// new as this answer, and keeps it. The caller gets what the row holds.
	written, err := s.saveRowIf(row, isNew, read)
	if err != nil {
		return nil, err
	}
	if !written {
		current, _, err := s.loadRow(customerID, provider, adapter)
		return current, err
	}
	// An unchanged posture is not news. Publishing it anyway would wake every
	// idle app on a reconciliation sweep; a changed one is, status or not.
	if row.Posture() != previous {
		s.publish(row, "")
		s.notifier.KycUpdated(previous.Status, row)
	}
	s.reconcile(row)
	return row, nil
}

// Get returns the local KYC state. A user who never touched a provider gets a
// synthesized not-started row: the app needs something to render, and a read
// must not write.
func (s *ProviderKycService) Get(customerID uint, provider string) (*models.CustomerProviderKyc, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	row, err := s.kycRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return nil, fmt.Errorf("kyc: lookup: %w", err)
	}
	if row == nil {
		row = s.newRow(customerID, provider, adapter)
	}
	s.markRow(row)
	return row, nil
}

// List returns the user's KYC state at every eligible provider — the same set
// the submissions fan out to — in provider order, each marked with whether it
// still owes the whole application.
//
// Eligible, not touched: a disabled provider's row is left out, because the
// app builds its status, gate and wizard steps from this list, and a step owed
// to a provider no submission reaches can never be satisfied. For the same
// reason a provider that does not serve the person the retained profile
// declares is left out — a passport's Ozav row included (servingRetained). An
// eligible provider the user never touched is synthesized as not_started,
// because that is how a provider enabled after the user's first KYC shows up
// at all.
func (s *ProviderKycService) List(customerID uint) ([]models.CustomerProviderKyc, error) {
	rows, _, err := s.list(customerID)
	if err != nil {
		return nil, err
	}
	s.markNeedsApplication(customerID, rows)
	return rows, nil
}

// list is List before the rows are marked with what they owe, keeping every
// row the user has stored, eligible or not, for the overview's "has this user
// ever submitted" question. Each row it answers already carries the rails its
// provider does not open to the retained person (RailsNotEligible), so the
// rails_open both listings show agrees with the router rather than with the
// endorsements alone.
func (s *ProviderKycService) list(customerID uint) ([]models.CustomerProviderKyc, []models.CustomerProviderKyc, error) {
	// Quiet: this runs on every screen load; the fan-outs log a mode mismatch
	// when it is about to matter.
	providers, who, err := servingRetained(s.config, s.factory, s.appEnv, s.persons, customerID)
	if err != nil {
		return nil, nil, fmt.Errorf("kyc: list: %w", err)
	}
	stored, err := s.kycRepo.ListByCustomer(customerID)
	if err != nil {
		return nil, nil, fmt.Errorf("kyc: list: %w", err)
	}
	byProvider := make(map[string]*models.CustomerProviderKyc, len(stored))
	for i := range stored {
		byProvider[stored[i].Provider] = &stored[i]
	}
	rows := make([]models.CustomerProviderKyc, 0, len(providers))
	for _, adapter := range providers {
		code := adapter.Code()
		row, ok := byProvider[code]
		if !ok {
			row = s.newRow(customerID, code, adapter)
		}
		rows = append(rows, *row)
		rows[len(rows)-1].RailsNotEligible = railsNotOpened(who, adapter)
	}
	return rows, stored, nil
}

// Overview is the one KYC state the user sees, over the rows List answers.
//
// A provider with a submission still staged counts as working whatever its row
// says. The staging's in-flight mark is the signal: it is written when the
// request stages the payload and cleared when the worker finishes with it, and
// in between the provider may already read `incomplete`, asking for the files
// sitting in the queue (Bridge, right after the application lands).
func (s *ProviderKycService) Overview(customerID uint) (*OverviewView, error) {
	rows, stored, err := s.list(customerID)
	if err != nil {
		return nil, err
	}
	// Strict where List is lenient: unmarked, a user owing the whole
	// application owes nothing the overview can see, and a fresh user would
	// read as under review.
	links, err := s.integRepo.ListByCustomer(customerID)
	if err != nil {
		return nil, fmt.Errorf("kyc: overview: read integrations: %w", err)
	}
	MarkNeedsApplication(rows, links, s.factory)
	inFlight := make(map[string]bool, len(rows))
	for i := range rows {
		inFlight[rows[i].Provider] = s.staging.InFlightKind(customerID, rows[i].Provider) != ""
	}
	overall, err := s.businessOverall(customerID, ComputeOverall(rows, inFlight, submittedBefore(stored), time.Now()))
	if err != nil {
		return nil, fmt.Errorf("kyc: overview: %w", err)
	}
	return &OverviewView{Overall: overall, CheckedAt: time.Now().UTC(), Providers: rows}, nil
}

// submittedBefore reports whether the user ever sent a provider anything: a
// row past not_started, or one that recorded why a submission failed.
func submittedBefore(stored []models.CustomerProviderKyc) bool {
	for i := range stored {
		if stored[i].KycStatus != models.KycStatusNotStarted || stored[i].LastErrorCode != nil {
			return true
		}
	}
	return false
}

// markNeedsApplication marks rows from the user's integrations. A read failure
// logs and leaves every row unmarked — the listing as it read before
// NeedsApplication existed — rather than failing a screen load over it.
func (s *ProviderKycService) markNeedsApplication(customerID uint, rows []models.CustomerProviderKyc) {
	links, err := s.integRepo.ListByCustomer(customerID)
	if err != nil {
		facades.Log().Errorf("kyc: reading integrations for customer %d: %v", customerID, err)
		return
	}
	MarkNeedsApplication(rows, links, s.factory)
}

// Requirements asks the provider what the user still owes.
//
// A provider holding no customer is not asked: there is nothing upstream to
// answer (Bridge refuses such a call with 409), and it owes the whole
// application. The per-provider wizard — the entrance a provider enabled after
// the user's first KYC opens — reads its steps from here, so it answers.
func (s *ProviderKycService) Requirements(ctx context.Context, customerID uint, provider string) (*RequirementsView, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	link, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return nil, fmt.Errorf("kyc: lookup integration: %w", err)
	}
	if !hasExternalCustomer(link) {
		return s.applicationRequirements(customerID, provider, adapter)
	}
	ref := ramp.NewCustomerRef(customerID, link.ExternalCustomerID)
	result, err := adapter.GetRequirements(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("kyc: requirements at %s: %w", provider, err)
	}
	// Raw stops here: it is the provider's snapshot of a person.
	steps := PendingStepsFromMissing
	if s.isBusiness(customerID) {
		steps = BusinessPendingStepsFromMissing
	}
	return &RequirementsView{
		Provider:     provider,
		Status:       result.Status,
		Missing:      result.Missing,
		PendingSteps: steps(result.Missing),
		Endorsements: result.Endorsements,
	}, nil
}

// applicationRequirements is Requirements' answer for a provider with no
// customer: its stored state, owing the application.
func (s *ProviderKycService) applicationRequirements(customerID uint, provider string, adapter ramp.ProviderAdapter) (*RequirementsView, error) {
	row, err := s.kycRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return nil, fmt.Errorf("kyc: lookup: %w", err)
	}
	if row == nil {
		row = s.newRow(customerID, provider, adapter)
	}
	markApplication(row, false, s.factory)
	return &RequirementsView{
		Provider:         provider,
		Status:           row.KycStatus,
		PendingSteps:     PendingStepsFor(row),
		NeedsApplication: row.NeedsApplication,
	}, nil
}

// PendingSteps is what one provider still wants from this user, as the wizard's
// step ids. It satisfies the ramps package's PendingStepsSource.
//
// It reads the stored row rather than calling GetRequirements, and that is the
// whole design of the port: it answers a refusal the user is already waiting
// on, so putting a provider round trip in that path would trade an honest
// 409 for a timeout. It is the same derivation KycResource renders, which is
// what keeps the wizard opened from an error and the wizard opened from the
// listing showing the same steps.
//
// A read failure answers with no steps and a log line. The refusal itself has
// already been decided by the caller; losing the steps degrades the screen,
// and failing here would replace a 409 the user can act on with a 500 nobody
// can.
//
// A provider the user never touched owes the whole application: it is how a
// provider enabled after the user's first KYC first refuses them.
func (s *ProviderKycService) PendingSteps(customerID uint, provider string) []string {
	row, err := s.kycRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		facades.Log().Errorf("kyc: pending steps for customer %d at %s: %v", customerID, provider, err)
		return nil
	}
	if row == nil {
		adapter, err := s.factory.Get(provider)
		if err != nil {
			return nil
		}
		row = s.newRow(customerID, provider, adapter)
	}
	s.markRow(row)
	return PendingStepsFor(row)
}

// markRow is markNeedsApplication for one row. A read failure logs and leaves
// the row unmarked, for the same reason.
func (s *ProviderKycService) markRow(row *models.CustomerProviderKyc) {
	link, err := s.integRepo.FindByCustomerAndProvider(row.CustomerID, row.Provider)
	if err != nil {
		facades.Log().Errorf("kyc: reading integration for customer %d at %s: %v", row.CustomerID, row.Provider, err)
		return
	}
	markApplication(row, hasExternalCustomer(link), s.factory)
}

// Profile returns the user's retained profile fields, for pre-filling a
// wizard a new provider opens. (nil, nil) means the user has never submitted
// one — GET /api/kyc/profile reads that as 204, not 404: it is not that the
// resource does not exist, it is that there is nothing there yet.
func (s *ProviderKycService) Profile(customerID uint) (*ramp.Profile, error) {
	profile, err := s.profileRepo.Find(customerID)
	if err != nil {
		return nil, fmt.Errorf("kyc: load profile: %w", err)
	}
	return profile, nil
}
