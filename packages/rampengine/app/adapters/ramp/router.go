package ramp

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// Router picks the provider for a rail. The client never chooses: the choice is
// the intersection of the admin's configured order, what is actually
// implemented, what the person or company may be offered there, and what this
// particular customer is cleared for.
type Router struct {
	factory *Factory
	config  *Config
	integ   repositories.CustomerProviderIntegrationRepository
	kycs    repositories.CustomerProviderKycRepository
	// persons says who the customer declared itself to be — the retained tax
	// identity and residence, or the company's — which decides what each
	// provider may open to it at all (RailsOf).
	persons Persons
	appEnv  string
}

// RouterDeps is what a Router reads, filled by field name where it is wired.
type RouterDeps struct {
	Factory      *Factory
	Config       *Config
	Integrations repositories.CustomerProviderIntegrationRepository
	Kyc          repositories.CustomerProviderKycRepository
	Persons      Persons
	// AppEnv is compared against each provider's mode, so a misconfigured
	// provider is refused at the point money would move rather than only
	// reported at boot.
	AppEnv string
}

// NewRouter builds a Router.
func NewRouter(d RouterDeps) *Router {
	return &Router{factory: d.Factory, config: d.Config, integ: d.Integrations, kycs: d.Kyc,
		persons: d.Persons, appEnv: d.AppEnv}
}

// Candidate is an eligible provider and the customer reference to call it with.
type Candidate struct {
	Adapter ProviderAdapter
	Ref     CustomerRef
}

func (c Candidate) Code() ProviderCode { return c.Adapter.Code() }

// rejectionRank orders the refusals by how ACTIONABLE they are, not by how far
// the provider got. The distinction is the whole point: a provider that refused
// the customer for good got further than one the user has not linked, and
// reporting it would hand the user a dead end while a walkable path sat one
// position down the rail order.
type rejectionRank int

const (
	// rejectedUnavailable is a provider the user could do nothing about: not
	// configured, disabled, mode mismatch, a stub, or one that simply does not
	// serve the capability asked for. Nothing here names a provider, because
	// naming one would invite the user to fix an admin's decision.
	rejectedUnavailable rejectionRank = iota
	// rejectedNotEligible is a provider that will not open this rail to this
	// person: the tax identity and residence they declared do not open it
	// there (RailsOf), or the provider approved them and said the rail is not
	// available where they live. Nothing the wizard collects changes it, so
	// it ranks below every other refusal about the user — it is the answer
	// only when nothing more actionable is left, and a provider that refused
	// the customer says more — and above unavailable, because it says
	// something true about the user that an empty order does not.
	rejectedNotEligible
	// rejectedKycRejected is a provider that refused this customer. It is
	// nameable — the client says "not available through this provider" — but
	// deliberately ranks BELOW the two actionable ones.
	rejectedKycRejected
	// rejectedNoIntegration is a usable provider the user has not linked.
	rejectedNoIntegration
	// rejectedKycPending is a linked provider whose endorsements do not cover
	// the capability yet: actionable, and the most informative of the four.
	rejectedKycPending
)

// rejection is how far the best rejected candidate got, and who it was.
//
// It carries the provider because the refusal has to name one: telling a user
// that nobody serves USDT, when the provider that would have served it is one
// KYC step away, is the defect this type exists to fix.
type rejection struct {
	rank     rejectionRank
	provider ProviderCode
	// status is the KycStatus of that provider's row, when there is one.
	status string
}

// unavailable is the zero rejection: nothing survived, and nothing is nameable.
var unavailable = rejection{rank: rejectedUnavailable}

// FailoverPolicy decides whether the router may try the next provider in the
// rail order after a failed call.
type FailoverPolicy func(err error) bool

// FailoverOnAnyError is the estimate policy. An estimate moves no money and
// creates nothing upstream, so any failure may be retried on the next provider.
func FailoverOnAnyError(err error) bool { return err != nil }

// FailoverOnUnsupported is the standing-account policy. It advances only when
// the provider does not implement the port at all, which is a fact about the
// provider rather than an outcome of a call: nothing was asked of it, so the
// next one in the rail order is free to serve.
//
// Every other failure stops the chain: ProvisionVirtualAccount may have
// created the account before the error surfaced, and provisioning again
// elsewhere would hand one user two deposit codes — money paid into the one XIP
// stopped tracking arrives with no order to attach it to.
//
// A provider without the liquidation-address port is the same fact about the
// payout side, and fails over the same way.
func FailoverOnUnsupported(err error) bool {
	return errors.Is(err, ErrStandingAccountUnsupported) || errors.Is(err, ErrLiquidationAddressUnsupported)
}

// Resolve returns the first eligible provider for the capability, together with
// the CustomerRef the caller must pass into it.
//
// A compliance refusal comes back as *KycBlocked, unchanged through this
// signature. Callers that only need a code read it with errors.Is on the
// sentinel it wraps; the ones that have to name the provider — the HTTP
// envelope, the capabilities read model — reach it with errors.As.
func (r *Router) Resolve(ctx context.Context, customerID uint, cap Capability) (ProviderAdapter, CustomerRef, error) {
	candidates, reason, err := r.candidates(ctx, customerID, cap)
	if err != nil {
		return nil, CustomerRef{}, err
	}
	if len(candidates) == 0 {
		LogResolve(cap.Rail.String(), reason.provider, outcomeFor(reason), 0)
		return nil, CustomerRef{}, reasonError(reason)
	}
	LogResolve(cap.Rail.String(), candidates[0].Adapter.Code(), OutcomeOK, len(candidates))
	return candidates[0].Adapter, candidates[0].Ref, nil
}

// outcomeFor names why nothing survived eligibility. "No provider configured"
// and "the user has not finished KYC" look identical from outside and need
// completely different responses, so the reason is what gets logged.
func outcomeFor(reason rejection) Outcome {
	switch reason.rank {
	case rejectedKycPending:
		return OutcomeKycPending
	case rejectedKycRejected:
		return OutcomeKycRejected
	case rejectedNoIntegration:
		return OutcomeIntegrationRequired
	case rejectedNotEligible:
		return OutcomeRailNotEligible
	default:
		return OutcomeNoProvider
	}
}

// ResolveProvider returns one specific provider, applying the same eligibility
// checks but the person's: it is the path of what already exists — an order's
// status, the reconciler, a standing account's destination — which must
// finish even after the user switched their tax identity. A create that
// redeems an estimate is not that, and asks ResolveRedemption.
//
// The capability it evaluates names the rail and nothing else, on purpose:
// whatever it serves was settled when it was created, and re-deciding the
// token here would only create a second opportunity to disagree with it.
func (r *Router) ResolveProvider(ctx context.Context, customerID uint, rail Rail, code ProviderCode) (ProviderAdapter, CustomerRef, error) {
	return r.resolveProvider(ctx, customerID, rail, code, nil)
}

// ResolveRedemption is the sticky path of a create: once an estimate has
// chosen a provider, the create that redeems it must go to that same provider
// or not happen at all. It is ResolveProvider plus the person, read once: an
// estimate can outlive the declaration it was priced under — a Bridge estimate
// has no expiry — so a user who switched from a CPF to a passport since must
// not be placed a deposit the passport does not open.
func (r *Router) ResolveRedemption(ctx context.Context, customerID uint, rail Rail, code ProviderCode) (ProviderAdapter, CustomerRef, error) {
	who, err := r.persons.RetainedPerson(customerID)
	if err != nil {
		return nil, CustomerRef{}, err
	}
	return r.resolveProvider(ctx, customerID, rail, code, who)
}

// resolveProvider runs one provider through the eligibility chain for who.
func (r *Router) resolveProvider(
	ctx context.Context, customerID uint, rail Rail, code ProviderCode, who *Person,
) (ProviderAdapter, CustomerRef, error) {
	// A provider named here is one a quote already chose, and a quote of a
	// provider that prepares funding was only given to a caller that accepts
	// it: the preference of the estimate is not asked again.
	candidate, reason, err := r.evaluate(ctx, customerID, Capability{Rail: rail, PreparedFunding: true}, code, who)
	if err != nil {
		return nil, CustomerRef{}, err
	}
	if candidate == nil {
		return nil, CustomerRef{}, fmt.Errorf("provider %q: %w", code, reasonError(reason))
	}
	return candidate.Adapter, candidate.Ref, nil
}

// ResolveForSettlement resolves a provider to finish a movement the user has
// already paid for, WITHOUT consulting eligibility.
//
// It is the deliberate counterpart to ResolveProvider, and the difference is
// the question being asked. Eligibility answers "may this user start
// something": is the provider enabled, is its mode coherent with the
// environment, is the integration usable, does the KYC grant the rail. Every
// one of those is the right gate on a create and the wrong gate on a
// settlement — a customer suspended after paying a BR Code, or a provider an
// operator switched off during an incident, would strand crypto the provider is
// already holding, with each retry refusing identically and no path that ever
// resolves it.
//
// This is the same rule the deposit branch of the reconciler follows for the
// same reason, and the one CLAUDE.md states as "reading an account or a deposit
// back does not go through the router".
//
// What it still requires is a customer to name: without one there is no call to
// make, which is a failure rather than a skip.
func (r *Router) ResolveForSettlement(_ context.Context, customerID uint, code ProviderCode) (ProviderAdapter, CustomerRef, error) {
	adapter, err := r.factory.Get(code)
	if err != nil {
		return nil, CustomerRef{}, fmt.Errorf("settlement: resolve %s: %w", code, err)
	}
	integration, err := r.integ.FindByCustomerAndProvider(customerID, code)
	if err != nil {
		return nil, CustomerRef{}, fmt.Errorf("settlement: load %s integration: %w", code, err)
	}
	if integration == nil || integration.ExternalCustomerID == nil || *integration.ExternalCustomerID == "" {
		return nil, CustomerRef{}, fmt.Errorf("settlement: customer %d has no %s link: %w",
			customerID, code, ErrIntegrationRequired)
	}
	return adapter, NewCustomerRef(customerID, integration.ExternalCustomerID), nil
}

// ResolveWithCall walks the rail order and invokes call on each eligible
// provider until one succeeds or policy refuses to advance. It returns the
// provider that handled the call.
//
// The last error is returned as-is rather than joined: a caller mapping it to
// an HTTP response needs the reason the chosen provider failed, not a summary
// of every provider that was tried.
func (r *Router) ResolveWithCall(
	ctx context.Context,
	customerID uint,
	cap Capability,
	policy FailoverPolicy,
	call func(context.Context, ProviderAdapter, CustomerRef) error,
) (ProviderCode, error) {
	rail := cap.Rail
	candidates, reason, err := r.candidates(ctx, customerID, cap)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		LogResolve(rail.String(), reason.provider, outcomeFor(reason), 0)
		return "", reasonError(reason)
	}

	var lastErr error
	for index, candidate := range candidates {
		if index > 0 {
			// Failover should be rare. A rise in this line means the head of the
			// rail order is unhealthy, and it is the signal to reorder the rail
			// before users notice.
			LogFailover(rail.String(), candidates[index-1].Code(), candidate.Code(), lastErr.Error())
		}
		lastErr = call(ctx, candidate.Adapter, candidate.Ref)
		if lastErr == nil {
			LogResolve(rail.String(), candidate.Code(), OutcomeOK, len(candidates))
			return candidate.Code(), nil
		}
		if !policy(lastErr) {
			return candidate.Code(), lastErr
		}
	}
	return "", lastErr
}

// candidates returns every eligible provider for the rail, in the admin's
// configured order. When none qualify, the second return says how far the best
// rejected candidate got.
//
// Who the person declared is read here, once for the whole order: it is a
// fact about the user, not about any one provider.
func (r *Router) candidates(ctx context.Context, customerID uint, cap Capability) ([]Candidate, rejection, error) {
	policy, err := r.config.Rail(cap.Rail)
	if err != nil {
		return nil, unavailable, err
	}
	// An empty order means the rail is not routable — the shape a rail closed
	// for business takes now that every rail resolves to an effective value.
	// Failing here is the safe direction.

	who, err := r.persons.RetainedPerson(customerID)
	if err != nil {
		return nil, unavailable, err
	}

	var candidates []Candidate
	best := unavailable
	for _, code := range policy.Providers {
		candidate, reason, err := r.evaluate(ctx, customerID, cap, code, who)
		if err != nil {
			return nil, unavailable, err
		}
		if candidate != nil {
			candidates = append(candidates, *candidate)
			continue
		}
		// Strictly greater: two providers refused the same way resolve to the
		// first in the rail order, which is the tie-break the admin chose and
		// the one the rest of the stack already uses.
		if reason.rank > best.rank {
			best = reason
		}
	}
	return candidates, best, nil
}

// evaluate runs one provider through the eligibility chain. A nil candidate
// with a nil error means "not eligible", and the rejection says why. who is
// the person the resolution is for, nil when it is not asked (ResolveProvider)
// or they retained no profile.
func (r *Router) evaluate(ctx context.Context, customerID uint, cap Capability, code ProviderCode, who *Person) (*Candidate, rejection, error) {
	state, err := r.config.Provider(code)
	if err != nil {
		return nil, unavailable, err
	}
	if !state.IsUsable() {
		return nil, unavailable, nil
	}
	// A provider whose mode disagrees with the environment is refused here,
	// before any call: this is the point where money would move.
	if err := ValidateMode(state.Mode, r.appEnv); err != nil {
		return nil, unavailable, nil
	}
	// Stubs are skipped without being called, so they never consume a position
	// in the failover chain.
	if !r.factory.IsImplemented(code) {
		return nil, unavailable, nil
	}
	adapter, err := r.factory.Get(code)
	if err != nil {
		return nil, unavailable, nil
	}
	// The capability filter sits BEFORE the integration and the KYC, and that
	// order is the design. A provider that does not serve the token could never
	// have redeemed the quote, so its KYC state is not a step the user should
	// be sent to finish — and leaving it in the running is what made the
	// provider that WOULD serve the token unreportable.
	if !adapter.Supports(cap) {
		return nil, unavailable, nil
	}
	// A client that cannot sign is never routed to funding it cannot do: on
	// Pix, a provider that prepares the funding of a payout waits for a caller
	// that said it accepts that. The bank rails have no other kind.
	if cap.Rail == RailPixPayout && !cap.PreparedFunding {
		if _, prepares := adapter.(OfframpPrepareAdapter); prepares {
			return nil, unavailable, nil
		}
	}
	// Then whether the provider would open the rail to this person at all,
	// for the same reason: a provider that will not take their deposit is not
	// one to send them to link or finish the KYC of — and its endorsements,
	// read below, may clear a rail its own policy does not open to them
	// (Bridge opens a BRL account for a passport).
	if !who.Opens(adapter, cap.Rail) {
		return nil, rejection{rank: rejectedNotEligible, provider: code}, nil
	}

	integration, err := r.integ.FindByCustomerAndProvider(customerID, code)
	if err != nil {
		return nil, unavailable, err
	}
	if integration == nil || !integration.IsUsable() {
		return nil, rejection{rank: rejectedNoIntegration, provider: code}, nil
	}

	kyc, err := r.kycs.FindByCustomerAndProvider(customerID, code)
	if err != nil {
		return nil, unavailable, err
	}
	// The guard is the endorsement, not the status: a row marked rejected whose
	// endorsements still cover the rail routes, and the status only decides
	// which refusal to report once the endorsement check has already failed.
	if kyc == nil || !kyc.SupportsRail(cap.Rail.String()) {
		return nil, kycRejection(code, kyc, cap.Rail), nil
	}

	// The rejection is only meaningful when the candidate is nil; on success it
	// is the zero value and callers ignore it.
	return &Candidate{
		Adapter: adapter,
		Ref:     CustomerRef{CustomerID: customerID, ExternalID: *integration.ExternalCustomerID},
	}, unavailable, nil
}

// kycRejection classifies a compliance refusal. A missing row is not_started —
// the user has simply not begun — and only an explicit `rejected` is the dead
// end, because every other status is something the wizard can still move.
//
// An approved row whose rail the provider says is not available where the
// customer lives is neither: the provider decided, about the rail rather than
// the customer, and a wizard opened over it would collect nothing that
// changes the answer.
func kycRejection(code ProviderCode, kyc *models.CustomerProviderKyc, rail Rail) rejection {
	if kyc == nil {
		return rejection{rank: rejectedKycPending, provider: code, status: models.KycStatusNotStarted}
	}
	if kyc.KycStatus == models.KycStatusRejected {
		return rejection{rank: rejectedKycRejected, provider: code, status: kyc.KycStatus}
	}
	if kyc.KycStatus == models.KycStatusApproved && kyc.RailNotAvailable(rail.String()) {
		return rejection{rank: rejectedNotEligible, provider: code, status: kyc.KycStatus}
	}
	return rejection{rank: rejectedKycPending, provider: code, status: kyc.KycStatus}
}

// reasonError turns the most actionable rejection into the error the client
// sees. Reporting the most actionable one matters: telling a user to link an
// account they already linked, because some other provider in the order was
// never configured, sends them down the wrong path — and telling them a
// provider refused them, while another is one step from serving, is worse.
//
// The two compliance refusals come back as *KycBlocked so the layer above can
// name the provider. PendingSteps is left empty here: what a provider still
// wants is the KYC package's vocabulary, and this package must never import it.
// A rail the person is not eligible for names none: there is no provider to
// send them to.
func reasonError(reason rejection) error {
	switch reason.rank {
	case rejectedKycPending:
		return &KycBlocked{Provider: reason.provider, Status: reason.status, Err: ErrKycPending}
	case rejectedKycRejected:
		return &KycBlocked{Provider: reason.provider, Status: reason.status, Err: ErrKycRejected}
	case rejectedNoIntegration:
		return ErrIntegrationRequired
	case rejectedNotEligible:
		return ErrRailNotEligible
	default:
		return ErrNoProviderAvailable
	}
}
