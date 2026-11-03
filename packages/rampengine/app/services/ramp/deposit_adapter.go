package ramp

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// ResolveDepositAdapter resolves the provider for a deposit without routing.
//
// The router answers "may this user transact on this rail" and refuses on an
// inactive integration or a lapsed endorsement. Re-reading a deposit is
// bookkeeping, not transacting, and gating it on eligibility would freeze
// exactly the orders the sweep exists to unstick — money Bridge already took.
//
// The webhook path is not gated either, so gating here would make the two
// disagree about the same row. The transfer branch keeps routing: an order is
// asked about under the rail it was placed on, and changing that is a separate
// decision about a flow already in production.
func ResolveDepositAdapter(
	factory *ramp.Factory,
	integRepo repositories.CustomerProviderIntegrationRepository,
	tx *models.Transaction,
) (ramp.VirtualAccountAdapter, ramp.CustomerRef, error) {
	standing, err := virtualAccountAdapter(factory, tx.Provider)
	if err != nil {
		return nil, ramp.CustomerRef{}, fmt.Errorf("ramp: %w", err)
	}

	integration, err := integRepo.FindByCustomerAndProvider(tx.CustomerID, tx.Provider)
	if err != nil {
		return nil, ramp.CustomerRef{}, fmt.Errorf("ramp: load integration: %w", err)
	}
	if integration == nil || integration.ExternalCustomerID == nil || *integration.ExternalCustomerID == "" {
		return nil, ramp.CustomerRef{}, fmt.Errorf(
			"ramp: order %s has no %s customer to ask", tx.ExternalID, tx.Provider)
	}
	return standing, ramp.NewCustomerRef(tx.CustomerID, integration.ExternalCustomerID), nil
}

// virtualAccountAdapter resolves a provider's standing-account port.
//
// A row or an event naming a provider whose adapter cannot answer for standing
// accounts is an operational anomaly worth surfacing, not something to skip in
// silence. ErrStandingAccountUnsupported stays wrapped: the client ramps
// controllers map that sentinel with errors.Is, so a bare error here would
// change an HTTP status.
func virtualAccountAdapter(factory *ramp.Factory, provider ramp.ProviderCode) (ramp.VirtualAccountAdapter, error) {
	adapter, err := factory.Get(provider)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", provider, err)
	}
	return ramp.StandingAccount(adapter)
}
