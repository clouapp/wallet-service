// Package providers is the admin's view of one user's place in the payment
// provider stack: the operator actions on their integration and KYC.
//
// Per-provider credentials and per-rail routing are settings groups now, served
// by /admin/api/settings/{group}.
package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/goravel/framework/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	auditdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/audit"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/providers"
	clientdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/admin/audit"
	customerssvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/customers"
	integrationsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/integration"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// ErrExternalCustomerNotThisUser refuses a relink whose provider-side customer cannot
// be shown to belong to the user it would be written on.
//
// It is a refusal, not a warning, because the failure it prevents is silent:
// a mistyped id links successfully, and every refresh from then on copies
// another person's compliance state — status, requirements, rejection reasons
// — onto this user's row, which their app renders as their own. There is no
// later error to notice.
var ErrExternalCustomerNotThisUser = errors.New("admin providers: customer does not belong to this user")

// ErrUserNotFound is an act that opens or advances a user's standing at a
// provider — start, hosted link, simulated approval or deposit, relink — for a
// user id no live account holds: none ever did, or the account was deleted.
var ErrUserNotFound = errors.New("admin providers: user not found")

// ErrExternalCustomerLinkedElsewhere refuses a link to a provider-side customer
// another account's live link holds.
var ErrExternalCustomerLinkedElsewhere = repositories.ErrExternalCustomerLinkedElsewhere

// ErrCustomerClosed refuses opening or advancing a closed company's standing
// at a provider — a start, a hosted link, a simulated approval, a relink —
// with the code its own writes answer (contract §1). Cleaning it up (reset,
// refresh, unlink) still runs.
var ErrCustomerClosed = customerssvc.ErrCustomerClosed

// Service drives the admin operator actions on one holder's provider state.
//
// A route names the holder (dto.HolderDTO): /users/{id} the account's
// individual customer, whose id is the account's (KYB API contract §12.8),
// /customers/{id} the customer itself (§12.5). Every KYC and integration call
// is for the holder; the account an act needs — the e-mail a relink proves,
// the hosted page, the rail flags — is a person's own, or a company's owner.
type Service struct {
	integRepo repositories.CustomerProviderIntegrationRepository
	kycRepo   repositories.CustomerProviderKycRepository
	userRepo  repositories.UserRepository
	callRepo  repositories.ProviderCallRepository
	// destRepo holds the payout destinations the provider registered for a
	// user. A relink is the one action that invalidates all of them at once —
	// see RelinkUserIntegration.
	destRepo    repositories.OfframpDestinationRepository
	integration *integrationsvc.Service
	kyc         *kycsvc.ProviderKycService
	// capabilities is the same read model GET /api/kyc/capabilities computes
	// for the client — the explorer surfaces it rather than a parallel
	// calculation, so the two can never disagree about why a user is stuck.
	capabilities *kycsvc.CapabilitiesService
	factory      *ramp.Factory
	config       *ramp.Config
	appEnv       string
	// offramp is the client off-ramp service, reused as-is for
	// SimulateDeposit. Its guards (production refusal, deposit-mode
	// check, idempotency) are not duplicated here — see that method.
	offramp *clientramps.OfframpService
	// accounts and transactions answer whether money is in flight at a
	// provider, which refuses an unlink.
	accounts     repositories.CustomerVirtualAccountRepository
	transactions repositories.TransactionRepository
	// audit is the trail every operator act here lands on.
	audit *audit.Service
	// features and webhooks feed the health read: the bank-payout switch and
	// the last delivery of a provider.
	features contracts.Feature
	webhooks repositories.WebhookEventRepository
	// locker takes the per-user provision lock around an unlink.
	locker locking.Locker
}

// Deps is what Service is built from.
type Deps struct {
	Integrations repositories.CustomerProviderIntegrationRepository
	Kyc          repositories.CustomerProviderKycRepository
	Users        repositories.UserRepository
	Calls        repositories.ProviderCallRepository
	Destinations repositories.OfframpDestinationRepository
	Integration  *integrationsvc.Service
	KycService   *kycsvc.ProviderKycService
	Capabilities *kycsvc.CapabilitiesService
	Factory      *ramp.Factory
	Config       *ramp.Config
	AppEnv       string
	Offramp      *clientramps.OfframpService
	// VirtualAccounts and Transactions refuse an unlink while money is in
	// flight at the provider.
	VirtualAccounts repositories.CustomerVirtualAccountRepository
	Transactions    repositories.TransactionRepository
	Audit           *audit.Service
	Locker          locking.Locker
	Features        contracts.Feature
	Webhooks        repositories.WebhookEventRepository
}

// NewService wires the admin provider service.
func NewService(d Deps) *Service {
	return &Service{
		integRepo: d.Integrations, kycRepo: d.Kyc, userRepo: d.Users, callRepo: d.Calls,
		destRepo:    d.Destinations,
		integration: d.Integration, kyc: d.KycService, capabilities: d.Capabilities,
		factory: d.Factory, config: d.Config, appEnv: d.AppEnv,
		offramp:  d.Offramp,
		accounts: d.VirtualAccounts, transactions: d.Transactions, audit: d.Audit, locker: d.Locker,
		features: d.Features, webhooks: d.Webhooks,
	}
}

// AccountHolder is the holder /users/{id} names: the account's individual
// customer, whose id is the account's, as every account holds it — read
// without a query, as the routes did before customers.
func AccountHolder(userID uint) dto.HolderDTO {
	return dto.HolderDTO{Customer: &models.Customer{
		Model: orm.Model{ID: userID}, Type: models.CustomerTypeIndividual, Status: models.CustomerStatusActive,
	}}
}

// UserIntegrations is the support view of one holder across providers.
type UserIntegrations struct {
	Integrations []models.CustomerProviderIntegration
	Kycs         []models.CustomerProviderKyc
	// Capabilities is nil when it could not be computed.
	Capabilities *kycsvc.CapabilitiesView
	// Overview is the one KYC state the user's app shows (GET
	// /api/kyc/overview), nil when it could not be computed.
	Overview *kycsvc.OverviewView
}

// ListIntegrations is the support view of one holder's provider links, now
// carrying the same capability read model and overall KYC state the client
// sees — which capabilities this holder cannot use right now, why, and what
// the app tells them about it. A company's capabilities are read with its
// owner's rail flags, as its owner operates it.
func (s *Service) ListIntegrations(ctx context.Context, holder dto.HolderDTO) (*UserIntegrations, error) {
	customerID := holder.Customer.ID
	integrations, kycs, err := s.held(customerID)
	if err != nil {
		return nil, err
	}
	// Marked with what the router refuses as rail_not_eligible, so the rails
	// an operator reads as open are the ones the user's app can use. A failed
	// profile read degrades to the endorsements alone rather than failing the
	// support view.
	if err := s.kyc.MarkRailsNotEligible(customerID, kycs); err != nil {
		facades.Log().WithContext(ctx).Errorf(
			"admin providers: mark rails not eligible for customer %d: %v", customerID, err)
	}
	// A capabilities or overview failure degrades to nil rather than failing
	// the whole endpoint. The resources are nil-safe by design for exactly
	// this case, and an operator looking at this tab because something is
	// broken is the one reader who must not lose integrations and kycs (both
	// already loaded successfully above) to an unrelated router/settings read
	// failing.
	view, err := s.capabilities.Compute(ctx, operatorOf(holder), customerID)
	if err != nil {
		facades.Log().WithContext(ctx).Errorf(
			"admin providers: compute capabilities for customer %d: %v", customerID, err)
		view = nil
	}
	overview, err := s.kyc.Overview(customerID)
	if err != nil {
		facades.Log().WithContext(ctx).Errorf(
			"admin providers: compute kyc overview for customer %d: %v", customerID, err)
		overview = nil
	}
	return &UserIntegrations{
		Integrations: integrations, Kycs: kycs, Capabilities: view, Overview: overview,
	}, nil
}

// HeldKyc is every KYC row customerID holds, at any provider, marked from its
// links as the compliance tab marks them, so each says whether it is
// terminal: what the customers listing sums up.
func (s *Service) HeldKyc(customerID uint) ([]models.CustomerProviderKyc, error) {
	_, kycs, err := s.held(customerID)
	return kycs, err
}

// held is customerID's links and KYC rows, the rows marked from the links as
// the client's listing marks its own: what a row owes is part of whether it
// is terminal.
func (s *Service) held(customerID uint) ([]models.CustomerProviderIntegration, []models.CustomerProviderKyc, error) {
	integrations, err := s.integRepo.ListByCustomer(customerID)
	if err != nil {
		return nil, nil, fmt.Errorf("admin providers: list integrations: %w", err)
	}
	kycs, err := s.kycRepo.ListByCustomer(customerID)
	if err != nil {
		return nil, nil, fmt.Errorf("admin providers: list kyc: %w", err)
	}
	kycsvc.MarkNeedsApplication(kycs, integrations, s.factory)
	return integrations, kycs, nil
}

// operatorOf is the account whose rail flags a holder's capabilities are read
// with: a person's own, a company's owner (0, none, when it has no owner).
func operatorOf(holder dto.HolderDTO) uint {
	if holder.Customer.Type != models.CustomerTypeBusiness {
		return holder.Customer.ID
	}
	if holder.Owner == nil {
		return 0
	}
	return holder.Owner.ID
}

// actingAccount is the account an act that opens or advances a holder's
// provider state acts with: a person's own, which a deletion makes
// ErrUserNotFound, or a company's owner, which a closed company never gets
// (ErrCustomerClosed). Reset, refresh and unlink do not ask: they are how a
// gone holder's provider state is cleaned up.
func (s *Service) actingAccount(holder dto.HolderDTO) (*models.User, error) {
	if holder.Customer.Type != models.CustomerTypeBusiness {
		return s.liveAccount(holder.Customer.ID)
	}
	if holder.Customer.Status == models.CustomerStatusClosed {
		return nil, ErrCustomerClosed
	}
	if holder.Owner == nil {
		return nil, fmt.Errorf("admin providers: business %d has no owner", holder.Customer.ID)
	}
	return holder.Owner, nil
}

// liveAccount loads the account an act opens or advances provider state
// for, refusing one that is deleted (the repository reads it as absent) with
// ErrUserNotFound.
//
// It is a check, not a lock: a deletion committing between it and the act
// leaves what a deletion always leaves — the account's links and rows are
// not removed by it — so the window adds no state a deleted account cannot
// already hold.
func (s *Service) liveAccount(userID uint) (*models.User, error) {
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return nil, fmt.Errorf("admin providers: load user %d: %w", userID, err)
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	return user, nil
}

// StartIntegration links a holder on the operator's behalf.
func (s *Service) StartIntegration(
	ctx context.Context, actor auditdto.ActorDTO, holder dto.HolderDTO, provider string,
) (*models.CustomerProviderIntegration, error) {
	if _, err := s.actingAccount(holder); err != nil {
		return nil, err
	}
	link, opened, err := s.integration.StartReporting(ctx, holder.Customer.ID, provider)
	if err != nil {
		return nil, err
	}
	// Answering the link already there changes nothing and records nothing.
	if opened {
		s.recordProviderAct(actor, audit.ActionIntegrationStarted, holder.Customer.ID, provider, nil)
	}
	return link, nil
}

// IssueHostedLink is the support escape hatch when a holder's document flow
// will not complete: an operator issues the provider's hosted page and shares
// it.
func (s *Service) IssueHostedLink(
	ctx context.Context, actor auditdto.ActorDTO, holder dto.HolderDTO, provider, fullName string,
) (*models.CustomerProviderKyc, error) {
	user, err := s.actingAccount(holder)
	if err != nil {
		return nil, err
	}
	userID := holder.Customer.ID
	row, err := s.kyc.HostedLink(ctx, user, userID, provider, clientdto.HostedLinkDTO{
		FullName: fullName,
		// Recorded on the row so the trail shows who asked for it and why.
		Reason: "support_request",
	})
	if err != nil {
		return nil, err
	}
	s.recordProviderAct(actor, audit.ActionKycHostedLinkIssued, userID, provider, nil)
	return row, nil
}

// RelinkUserIntegration points a user's integration at a provider-side customer
// id supplied by an operator.
//
// It exists for the orphan: a create that reached the provider and whose link
// row was never written leaves a customer nobody can reach — measured on
// 02/09, when Ozav answered 500 after materializing the record. The adapters
// recover that case themselves on the next submission, but only when the
// provider's own conflict names it; an operator holding the id from the
// provider's dashboard had no way to hand it over.
//
// Every path here is refusal-first. The id is read from the provider before
// anything is written, and the customer's own email must match the account's:
// a wrong id is not an error the operator would see, it is a successful write
// that starts copying a stranger's compliance state onto this user. An empty
// email from the provider proves nothing and is refused for the same reason.
//
// A user with no link at all — the orphan this was written for — has one
// created (adopted, under the lock Start takes), and the customer's KYC read
// back so the row appears. A customer another live link holds is refused
// (ErrExternalCustomerLinkedElsewhere): one customer is one account.
//
// A company proves the provider customer is its own by its document (KYB
// contract §12.5), and no provider gives it back: Bridge's business customer
// carries no tax id (sandbox probe 18/10/2026). So a company's relink and
// adoption are refused before anything is asked or written, never proved by
// an e-mail instead: a duplicate e-mail is never adopted (spec §2).
func (s *Service) RelinkIntegration(
	ctx context.Context, actor auditdto.ActorDTO, holder dto.HolderDTO, provider, externalCustomerID string,
) (*models.CustomerProviderIntegration, error) {
	externalCustomerID = strings.TrimSpace(externalCustomerID)
	if externalCustomerID == "" {
		return nil, fmt.Errorf("admin providers: relink: empty customer id")
	}
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	user, err := s.actingAccount(holder)
	if err != nil {
		return nil, err
	}
	if holder.Customer.Type == models.CustomerTypeBusiness {
		return nil, ErrExternalCustomerNotThisUser
	}
	userID := holder.Customer.ID

	// Read before write. A 404 here is the provider saying the id does not
	// exist, which is the common typo and must not reach the row.
	state, err := adapter.GetKyc(ctx, ramp.CustomerRef{CustomerID: userID, ExternalID: externalCustomerID})
	if err != nil {
		return nil, fmt.Errorf("admin providers: relink: read customer: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(state.CustomerEmail), strings.TrimSpace(user.Email)) {
		// The attempt this guard exists to stop, and the only trail it leaves:
		// the refusal answers 409 and nothing else records it. The customer id
		// and both emails stay out — they are the two people's own.
		facades.Log().WithContext(ctx).Warningf(
			"admin providers: relink refused for user %d at %s: the customer belongs to somebody else",
			userID, provider)
		return nil, ErrExternalCustomerNotThisUser
	}

	// An unlink landing between the read and the write leaves no link to
	// rewrite: the second pass finds none and adopts the customer.
	link, err := s.pointLink(ctx, actor, userID, provider, externalCustomerID)
	if errors.Is(err, repositories.ErrIntegrationGone) {
		link, err = s.pointLink(ctx, actor, userID, provider, externalCustomerID)
	}
	return link, err
}

// pointLink writes the customer id on the user's link, adopting the customer
// when there is no link, and records it. Pointing an active link at the id it
// already holds changes nothing and records nothing.
func (s *Service) pointLink(
	ctx context.Context, actor auditdto.ActorDTO, userID uint, provider, externalCustomerID string,
) (*models.CustomerProviderIntegration, error) {
	link, err := s.integRepo.FindByCustomerAndProvider(userID, provider)
	if err != nil {
		return nil, fmt.Errorf("admin providers: relink: load integration: %w", err)
	}
	if link == nil {
		adopted, created, err := s.integration.Adopt(ctx, userID, provider, externalCustomerID)
		if err != nil {
			return nil, fmt.Errorf("admin providers: relink: adopt customer: %w", err)
		}
		if created {
			s.readAdoptedCustomer(ctx, userID, provider)
			s.recordRelink(ctx, actor, userID, provider, "", externalCustomerID)
			return adopted, nil
		}
		link = adopted
	}

	previous := ""
	if link.ExternalCustomerID != nil {
		previous = *link.ExternalCustomerID
	}
	if previous == externalCustomerID && link.Status == models.IntegrationStatusActive {
		return link, nil
	}
	// The recorded off-ramp destinations name external accounts on the customer
	// being left, so they are worthless the moment the id changes — and nothing
	// else clears them: the next withdrawal finds the ref, sends it, and the
	// provider refuses it. Forgetting them first is what makes a failure here
	// harmless: the user stays on the customer the destinations belong to, and
	// the retry runs the same way. Written first, a failure would strand them,
	// because the retry would read the new id as the current one and keep the
	// unreachable rows. Re-submitting the same id keeps them: the customer is
	// the same, and re-registering a key it already holds is what fails.
	if previous != externalCustomerID {
		if err := s.destRepo.DeleteByCustomerAndProvider(userID, provider); err != nil {
			return nil, fmt.Errorf("admin providers: relink: forget offramp destinations: %w", err)
		}
	}
	link.ExternalCustomerID = &externalCustomerID
	link.Status = models.IntegrationStatusActive
	if err := s.integRepo.Update(link); err != nil {
		return nil, fmt.Errorf("admin providers: relink: save integration: %w", err)
	}
	s.recordRelink(ctx, actor, userID, provider, previous, externalCustomerID)
	return link, nil
}

// recordRelink puts the relink on the audit trail and in the log. The email
// that authorised it is deliberately absent from both — it is the user's.
func (s *Service) recordRelink(ctx context.Context, actor auditdto.ActorDTO, userID uint, provider, from, to string) {
	facades.Log().WithContext(ctx).Infof(
		"admin providers: relinked user %d at %s: %q -> %q", userID, provider, from, to)
	s.recordProviderAct(actor, audit.ActionIntegrationRelinked, userID, provider,
		map[string]any{"from": from, "to": to})
}

// readAdoptedCustomer reads an adopted customer's KYC from the provider so
// the row appears. The link is already written, so a failure is logged and
// the operator refreshes again.
func (s *Service) readAdoptedCustomer(ctx context.Context, userID uint, provider string) {
	if _, err := s.kyc.Refresh(ctx, userID, provider); err != nil {
		facades.Log().WithContext(ctx).Warningf(
			"admin providers: relink: refresh adopted customer for user %d at %s: %v", userID, provider, err)
	}
}

// recordProviderAct records an act that called a provider, once it
// succeeded: the provider side cannot be rolled back, so a failed insert is
// logged and the act's answer stands.
func (s *Service) recordProviderAct(
	actor auditdto.ActorDTO, action string, userID uint, provider string, metadata map[string]any,
) {
	s.audit.RecordIrreversible(actor, auditdto.EntryDTO{
		Action: action, SubjectType: audit.SubjectCustomers, SubjectID: userID, Provider: provider, Metadata: metadata,
	})
}

// providerCallPageSize bounds one page of the timeline.
//
// Newest-first and capped rather than paginated: the question this answers is
// "what happened to this person recently", and an operator who has to page
// backwards through a hundred entries is reading the wrong tool. If a case
// ever needs more, it needs a filter, not a longer list.
const providerCallPageSize = 100

// ListProviderCalls is one customer's provider timeline, newest first.
//
// It reads the calls XIP made about that holder — by whichever member — and the
// ones it decided not to make. The second half is the reason it exists: on 02/09 a user's documents
// reached this backend and were dropped by a filter one second later, and
// nothing anyone could open said so — the operator and the user both believed
// they were with the provider.
func (s *Service) ListProviderCalls(customerID uint) ([]models.ProviderCall, error) {
	calls, err := s.callRepo.ListByCustomer(customerID, providerCallPageSize)
	if err != nil {
		return nil, fmt.Errorf("admin providers: list calls: %w", err)
	}
	return calls, nil
}

// RefreshKyc re-reads a customer's compliance state from the provider.
func (s *Service) RefreshKyc(
	ctx context.Context, actor auditdto.ActorDTO, customerID uint, provider string,
) (*models.CustomerProviderKyc, error) {
	row, err := s.kyc.Refresh(ctx, customerID, provider)
	if err != nil {
		return nil, err
	}
	s.recordProviderAct(actor, audit.ActionKycRefreshed, customerID, provider, nil)
	return row, nil
}

// SimulateKycApproval calls the provider's sandbox KYC shortcut, then
// refreshes the local row. It is refused outside sandbox mode or in production.
func (s *Service) SimulateKycApproval(
	ctx context.Context, actor auditdto.ActorDTO, holder dto.HolderDTO, provider string,
) (*models.CustomerProviderKyc, error) {
	if _, err := s.actingAccount(holder); err != nil {
		return nil, err
	}
	userID := holder.Customer.ID
	state, err := s.config.Provider(ramp.ProviderCode(provider))
	if err != nil {
		return nil, fmt.Errorf("admin providers: load: %w", err)
	}
	if !s.factory.IsImplemented(provider) {
		return nil, fmt.Errorf("provider %q: %w", provider, ramp.ErrProviderNotImplemented)
	}
	if state.Mode != models.ProviderModeSandbox || s.appEnv == "production" {
		return nil, ramp.ErrSandboxOnly
	}
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	link, err := s.integRepo.FindByCustomerAndProvider(userID, provider)
	if err != nil {
		return nil, fmt.Errorf("admin providers: load integration for user %d at %s: %w", userID, provider, err)
	}
	if link == nil || link.ExternalCustomerID == nil {
		return nil, ramp.ErrIntegrationRequired
	}
	simulator, ok := adapter.(ramp.KycSimulator)
	if !ok {
		return nil, fmt.Errorf("admin providers: simulate kyc: %w", ramp.ErrSandboxOnly)
	}
	if err := simulator.SimulateKycApproval(ctx, ramp.CustomerRef{
		CustomerID: userID, ExternalID: *link.ExternalCustomerID,
	}); err != nil {
		return nil, fmt.Errorf("admin providers: simulate kyc: %w", err)
	}
	// Recorded once the provider approved: the refresh after it only reads.
	s.recordProviderAct(actor, audit.ActionKycApprovalSimulated, userID, provider, nil)
	return s.kyc.Refresh(ctx, userID, provider)
}

// SimulateDeposit completes an off-ramp order on an operator's behalf, for a
// holder who cannot supply their own token.
//
// This is a pure delegation to OfframpService.SimulateDeposit: production
// refusal, the deposit-mode check and the idempotency guard all live there.
// Reimplementing any of them here would create a second place deciding what
// counts as sandbox, and the two would drift.
//
// The order is the holder's own (an external id of another holder's order is
// order_not_found). A person's needs its account live, as before; a company's
// completes whatever its status, as its own app's simulated deposit does on a
// closed company — completing an order already placed opens nothing new.
func (s *Service) SimulateDeposit(
	ctx context.Context, actor auditdto.ActorDTO, holder dto.HolderDTO, externalID string,
) (*clientramps.OrderView, error) {
	customerID := holder.Customer.ID
	if holder.Customer.Type != models.CustomerTypeBusiness {
		if _, err := s.liveAccount(customerID); err != nil {
			return nil, err
		}
	}
	view, err := s.offramp.SimulateDeposit(ctx, customerID, externalID)
	if err != nil {
		return nil, err
	}
	s.recordProviderAct(actor, audit.ActionOfframpDepositSimulated, customerID, "",
		map[string]any{"order_id": externalID})
	return view, nil
}
