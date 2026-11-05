package ramp

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// ResolveDrainAdapter resolves the provider for a drain without routing, for
// the reason ResolveDepositAdapter gives: re-reading a payout the user already
// funded is bookkeeping, and gating it on eligibility would freeze exactly the
// orders the sweep exists to unstick. ErrLiquidationAddressUnsupported stays
// wrapped.
func ResolveDrainAdapter(
	factory *ramp.Factory,
	integRepo repositories.CustomerProviderIntegrationRepository,
	tx *models.Transaction,
) (ramp.LiquidationAddressAdapter, ramp.CustomerRef, error) {
	adapter, err := factory.Get(tx.Provider)
	if err != nil {
		return nil, ramp.CustomerRef{}, fmt.Errorf("ramp: resolve %s: %w", tx.Provider, err)
	}
	payout, err := ramp.PayoutAddress(adapter)
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
	return payout, ramp.NewCustomerRef(tx.CustomerID, integration.ExternalCustomerID), nil
}

// refreshDrain re-reads a USD payout out of its address's drains: a drain is
// not a transfer, and GetOrder would 404 on its id. The fold is the webhook's
// own (ApplyLiquidationDrain), so the two cannot disagree about one payout.
func (r *Reconciler) refreshDrain(ctx context.Context, tx *models.Transaction) (bool, error) {
	address, err := r.addresses.FindByID(*tx.LiquidationAddressID)
	if err != nil {
		return false, fmt.Errorf("ramp reconcile: load liquidation address: %w", err)
	}
	if address == nil {
		return false, fmt.Errorf("ramp reconcile: order %s names missing liquidation address %d",
			tx.ExternalID, *tx.LiquidationAddressID)
	}
	payout, ref, err := ResolveDrainAdapter(r.factory, r.integRepo, tx)
	if err != nil {
		return false, err
	}

	drain, err := payout.GetDrain(ctx, ref, address.ExternalAddressID, tx.GatewayOrderID)
	if err != nil {
		return false, err
	}
	previous := tx.Status
	if !DrainChangedFromProvider(tx, address, drain) {
		return false, nil
	}
	if !DrainMayMove(tx, drain) {
		return false, nil
	}

	ApplyLiquidationDrain(tx, address, drain)
	updated, err := r.txRepo.SaveProviderSnapshotIfStatus(tx, previous)
	if err != nil {
		return false, fmt.Errorf("ramp reconcile: persist drain refresh: %w", err)
	}
	return updated, nil
}
