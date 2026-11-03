package kyc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// CapabilityResolver is the read model's only view of eligibility. It is the
// exact method set of *ramp.Router.Resolve, narrowed to an interface so a
// unit test can supply a fake without building a real Router's collaborators.
//
// The read model is not a second eligibility engine: it interprets Resolve's
// answer (err == nil means available, errors.As gives the blocker) rather
// than re-deriving one, so a rule that changes in the router is felt here
// automatically instead of needing a parallel edit.
type CapabilityResolver interface {
	Resolve(ctx context.Context, customerID uint, cap ramp.Capability) (ramp.ProviderAdapter, ramp.CustomerRef, error)
}

// StepsSource mirrors app/services/client/ramps.PendingStepsSource — same
// shape, declared again here (under a different name, so mockery's
// filename-per-interface generation does not collide with that package's own
// mock) rather than imported, because this package and that one are siblings
// with no dependency between them today and a read-model port has no reason
// to be the thing that adds one. *ProviderKycService already satisfies both.
type StepsSource interface {
	PendingSteps(customerID uint, provider string) []string
}

// BlockedBy is why one capability is not available, and it is deliberately
// the same shape the HTTP error envelope carries (responses.ErrorResponse):
// the JIT screen renders identically whether it opened from a 409 or from
// this listing, which is the whole point of sharing the shape.
type BlockedBy struct {
	// Reason is the stable client error code — "kyc_pending", "kyc_rejected",
	// "integration_required", "rail_not_eligible", "no_provider_available" or
	// "rail_disabled".
	Reason string
	// Provider is empty for integration_required, rail_not_eligible,
	// no_provider_available and rail_disabled: the first three because the
	// router names no provider for them (see router.go's reasonError), the
	// last because an operator's veto is not any one provider's fault — this
	// view has no fact to add that it did not have.
	Provider string
	// PendingSteps is filled only when Reason is kyc_pending. A rejection is
	// a dead end — enrichKycRefusal (ramps.go) draws the same line — and
	// naming steps for a decision nothing the user types can change would
	// invite a retry that cannot succeed.
	PendingSteps []string
}

// CapabilityState is one (rail, token, chain) tuple annotated with whether
// the user may use it right now.
//
// Named State rather than reusing ramp.Capability's own name: Compute holds
// both a ramp.Capability (the request Resolve takes) and this (the answer),
// and the read model's own vocabulary should not collide with the one it is
// built on.
type CapabilityState struct {
	Rail      string
	Token     string
	Chain     string
	Available bool
	BlockedBy *BlockedBy
}

// PendingProvider is one eligible provider the user has not finished — or,
// for one still-open edge, has finished in a way that does not clear the
// endorsement a rail needs (see CapabilitiesView.Usable's own comment).
// Eligible, not merely enabled: a provider whose configured mode disagrees
// with APP_ENV never reaches this list either (see pendingProviders' own
// comment) — the same gate the KYC fan-out applies before it will touch that
// provider at all, so the banner never invites a tap toward one.
type PendingProvider struct {
	Provider     string
	Status       string
	PendingSteps []string
}

// CapabilitiesView is the read model GET /api/kyc/capabilities and the admin
// per-user compliance view both render.
type CapabilitiesView struct {
	// Usable is derived from Capabilities itself — at least one row with
	// Available true — never from "at least one provider approved". A KYC
	// row can be approved with zero approved endorsements (SupportsRail
	// fails every rail), and the two questions would then disagree; deriving
	// this one from the list itself is what keeps them from ever diverging.
	Usable           bool
	Capabilities     []CapabilityState
	PendingProviders []PendingProvider
}

// CapabilitiesService computes the read model. It holds no state of its own:
// every field is a narrow view of a collaborator another service in this
// package already owns.
type CapabilitiesService struct {
	resolver CapabilityResolver
	features contracts.Feature
	config   *ramp.Config
	factory  *ramp.Factory
	kycRepo  repositories.CustomerProviderKycRepository
	// integRepo answers which providers hold a customer for the customer: one
	// that does not owes the whole application, and its banner has to say so.
	integRepo repositories.CustomerProviderIntegrationRepository
	// persons says who the customer declared itself to be — the retained tax
	// identity and residence, or the company's — so the banner names only
	// providers that serve it.
	persons ramp.Persons
	steps   StepsSource
	// appEnv is this backend's own APP_ENV, the fact pendingProviders needs
	// to narrow config.EligibleProviders the same way the KYC fan-out does.
	appEnv string
}

// CapabilitiesDeps is what CapabilitiesService is built from.
type CapabilitiesDeps struct {
	Resolver     CapabilityResolver
	Features     contracts.Feature
	Config       *ramp.Config
	Factory      *ramp.Factory
	Kyc          repositories.CustomerProviderKycRepository
	Integrations repositories.CustomerProviderIntegrationRepository
	Persons      ramp.Persons
	Steps        StepsSource
	AppEnv       string
}

// NewCapabilitiesService wires the read model.
func NewCapabilitiesService(d CapabilitiesDeps) *CapabilitiesService {
	return &CapabilitiesService{
		resolver: d.Resolver, features: d.Features, config: d.Config, factory: d.Factory, kycRepo: d.Kyc,
		integRepo: d.Integrations, persons: d.Persons, steps: d.Steps, appEnv: d.AppEnv,
	}
}

// Compute builds the view for customerID as userID operates it: the product
// of the closed vocabulary above, each cell resolved through the very same
// Router.Resolve every operation is gated on. The rail switches are the
// user's, as every operation reads them; what is open is the customer's.
func (s *CapabilitiesService) Compute(ctx context.Context, userID, customerID uint) (*CapabilitiesView, error) {
	return s.ComputeFor(ctx, userID, customerID, false)
}

// ComputeFor is Compute for an app that declared whether it can sign the
// funding a provider prepares: without it a Pix payout served only by such a
// provider is not listed, as the estimate would not route to it.
func (s *CapabilitiesService) ComputeFor(ctx context.Context, userID, customerID uint, preparedFunding bool) (*CapabilitiesView, error) {
	view := &CapabilitiesView{}
	// blockedProviders is every provider named by a kyc_pending refusal while
	// walking the product below. It feeds pendingProviders' first of two
	// sources — see that method's own comment for the second, and for why one
	// alone is not enough.
	blockedProviders := map[string]bool{}
	stepsCache := map[string][]string{}

	rails := slices.Concat(ramp.AllRails(), ramp.BankRails())
	for _, rail := range rails {
		// Consulted once per rail, before any token in it: this is the same
		// veto every operation that actually creates something checks first
		// (requireRail, ramps.go), and checking it here first is what keeps
		// this listing from promising what the next call would refuse with
		// 503 rail_disabled. It fails closed on a gate error, same as
		// requireRail — a flag-store outage is the honest "not serving"
		// answer, not a reason to tell the user their KYC decided this.
		railOn, err := s.features.IsActive(ctx, rail.Flag(), models.UserFlagScope(userID))
		if err != nil {
			facades.Log().WithContext(ctx).Errorf("capabilities: %s switch for user %d: %v (reading closed)", rail, userID, err)
			railOn = false
		}

		for _, token := range models.RampTokens {
			cap := ramp.Capability{Rail: rail, Token: token, Chain: models.ChainSolana, PreparedFunding: preparedFunding}
			state := CapabilityState{Rail: rail.String(), Token: token, Chain: models.ChainSolana}

			if !railOn {
				// An operator's veto, not a user pendency: no provider, no
				// pending steps, and — critically — never fed into
				// blockedProviders below, since nothing here says anything
				// about any one provider's KYC state.
				blocked := BlockedBy{Reason: ramp.ErrorCode(ramp.ErrRailDisabled)}
				state.BlockedBy = &blocked
				view.Capabilities = append(view.Capabilities, state)
				continue
			}

			_, _, err := s.resolver.Resolve(ctx, customerID, cap)
			switch {
			case err == nil:
				state.Available = true
				view.Usable = true
			case isEligibilityRefusal(err):
				blocked := s.blockedByFor(err, customerID, stepsCache)
				state.BlockedBy = &blocked
				if blocked.Reason == "kyc_pending" && blocked.Provider != "" {
					blockedProviders[blocked.Provider] = true
				}
			default:
				// Not a compliance refusal: a genuine failure to even decide
				// (a settings read, a repository lookup). Degrading this to
				// "unavailable" would tell a user their KYC is the reason
				// when the actual cause is this side unable to answer.
				return nil, fmt.Errorf("kyc: capabilities: resolve %s/%s: %w", rail, token, err)
			}
			view.Capabilities = append(view.Capabilities, state)
		}
	}

	pending, err := s.pendingProviders(customerID, blockedProviders)
	if err != nil {
		return nil, err
	}
	view.PendingProviders = pending
	return view, nil
}

// pendingProviders feeds the banner. It is not derived from blockedProviders
// alone, because a provider fully shadowed by an already-approved one earlier
// in every rail's order — the ordinary shape of "an admin just enabled a
// second provider" (design §4.7) — never loses a Resolve call and so never
// appears in any capability's blocked_by. Every eligible, implemented
// provider whose own row is not yet approved or rejected is included whether
// or not it currently blocks anything; blockedProviders only widens that set
// for the approved-with-zero-endorsements edge (design decision on Usable),
// where the row's raw status would otherwise say "nothing to report".
//
// eligible, not enabled: servingRetained — the same narrowing List and
// ProviderKycService's fan-out entry points apply — drops a provider whose
// configured mode disagrees with APP_ENV, and one that does not serve the
// person the retained profile declares, before it ever reaches this list.
// Nothing here ever dials a provider, so a mismatch is not itself unsafe to
// read; what is unsafe is the banner this list feeds inviting a tap toward
// POST /api/kyc/{provider}/applications, the single-provider route that
// still does not check mode, and refuses a provider that does not serve the
// user. It reads quietly — it runs on every page load, not only when a real
// side effect was about to happen.
//
// One query answers every provider. This runs on every page load, and asking
// per provider cost two reads of the same row each — one for the status here,
// one more through PendingSteps for the steps — where ListByUser returns both
// facts for all of them at once. The steps are derived from that same row by
// the same function PendingSteps itself calls, so the banner and the wizard
// still read identically.
func (s *CapabilitiesService) pendingProviders(
	customerID uint, blockedProviders map[string]bool,
) ([]PendingProvider, error) {
	providers, _, err := servingRetained(s.config, s.factory, s.appEnv, s.persons, customerID)
	if err != nil {
		return nil, fmt.Errorf("kyc: capabilities: %w", err)
	}
	rows, err := s.kycRepo.ListByCustomer(customerID)
	if err != nil {
		return nil, fmt.Errorf("kyc: capabilities: load kyc rows: %w", err)
	}
	holds, known := s.externalCustomerHolders(customerID)
	byProvider := make(map[string]*models.CustomerProviderKyc, len(rows))
	for i := range rows {
		byProvider[rows[i].Provider] = &rows[i]
	}

	var out []PendingProvider
	for _, adapter := range providers {
		code := adapter.Code()
		row := byProvider[code]
		if row == nil {
			row = &models.CustomerProviderKyc{Provider: code, KycStatus: models.KycStatusNotStarted,
				Requirements: "[]", RejectionReasons: "[]"}
		}
		if known {
			markApplication(row, holds[code], s.factory)
		}
		status := row.KycStatus
		steps := PendingStepsFor(row)
		terminal := status == models.KycStatusApproved || status == models.KycStatusRejected
		if terminal && !blockedProviders[code] {
			continue
		}
		// Nor a partner that owes the user nothing — reviewing, or holding a
		// submission in flight. The banner promises steps, and a tap opened
		// that partner's wizard on its review page, inviting a second
		// submission to a provider already deciding.
		if len(steps) == 0 && !blockedProviders[code] {
			continue
		}
		out = append(out, PendingProvider{Provider: code, Status: status, PendingSteps: steps})
	}
	return out, nil
}

// externalCustomerHolders reads the user's integrations once for the whole listing.
// The bool is false when the read failed: the caller then marks nothing — the
// banner as it read before NeedsApplication existed, the same fallback
// ProviderKycService.List takes — rather than failing a page load over it.
func (s *CapabilitiesService) externalCustomerHolders(customerID uint) (map[string]bool, bool) {
	links, err := s.integRepo.ListByCustomer(customerID)
	if err != nil {
		facades.Log().Errorf("kyc: capabilities: reading integrations for customer %d: %v", customerID, err)
		return nil, false
	}
	return externalCustomerHolders(links), true
}

// isEligibilityRefusal reports whether err is one of the answers
// Router.Resolve gives when it decided, as opposed to when it failed to
// decide. It is the closed set reasonError (router.go) can produce: a
// *ramp.KycBlocked, or one of the three unnamed sentinels. Anything else is a
// genuine failure and must not be read as "the user is not eligible".
func isEligibilityRefusal(err error) bool {
	var blocked *ramp.KycBlocked
	if errors.As(err, &blocked) {
		return true
	}
	return errors.Is(err, ramp.ErrIntegrationRequired) || errors.Is(err, ramp.ErrRailNotEligible) ||
		errors.Is(err, ramp.ErrNoProviderAvailable)
}

// blockedByFor turns a Router.Resolve refusal into the read model's view of
// it. cache exists so a provider named on more than one capability (the usual
// case: a pending provider serving both rails) pays for PendingSteps'
// repository read once, not once per capability.
func (s *CapabilitiesService) blockedByFor(err error, customerID uint, cache map[string][]string) BlockedBy {
	blocked := BlockedBy{Reason: ramp.ErrorCode(err)}
	var kb *ramp.KycBlocked
	if !errors.As(err, &kb) {
		return blocked
	}
	blocked.Provider = kb.Provider
	if errors.Is(kb.Err, ramp.ErrKycPending) {
		blocked.PendingSteps = s.stepsFor(cache, customerID, kb.Provider)
	}
	return blocked
}

func (s *CapabilitiesService) stepsFor(cache map[string][]string, customerID uint, provider string) []string {
	if steps, ok := cache[provider]; ok {
		return steps
	}
	steps := s.steps.PendingSteps(customerID, provider)
	cache[provider] = steps
	return steps
}
