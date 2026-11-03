package ramps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

// Show returns one of the customer's on-ramp orders, refreshed against the
// provider when it is still in flight.
func (s *OnrampService) Show(ctx context.Context, customerID uint, externalID string) (*OrderView, error) {
	return s.show(ctx, customerID, externalID, models.TransactionTypeOnramp)
}

// Show returns one of the customer's off-ramp orders, refreshed against the
// provider when it is still in flight.
func (s *OfframpService) Show(ctx context.Context, customerID uint, externalID string) (*OrderView, error) {
	tx, err := s.load(customerID, externalID, models.TransactionTypeOfframp)
	if err != nil {
		return nil, err
	}
	if err := s.settleIfFundedThrottled(ctx, tx); err != nil {
		facades.Log().WithContext(ctx).With(map[string]any{"order": tx.ExternalID}).Warningf("ramps: offramp settle check: %v", err)
	}
	s.refreshInFlight(ctx, tx)
	return s.toOrderView(tx), nil
}

// List returns the customer's on-ramp orders, newest first.
func (s *OnrampService) List(customerID uint, page, perPage int) ([]OrderView, int64, error) {
	return s.page(repositories.TransactionListFilters{
		CustomerID: customerID, Type: models.TransactionTypeOnramp, Page: page, PerPage: perPage,
	})
}

// List returns the customer's off-ramp orders, newest first.
func (s *OfframpService) List(customerID uint, page, perPage int) ([]OrderView, int64, error) {
	return s.page(repositories.TransactionListFilters{
		CustomerID: customerID, Type: models.TransactionTypeOfframp, Page: page, PerPage: perPage,
	})
}

// page is the read behind every list. It does not refresh: a list is a summary
// screen, and firing one provider call per row to render it would multiply the
// upstream load by the page size for information the detail view already
// reconciles.
func (e *engine) page(filters repositories.TransactionListFilters) ([]OrderView, int64, error) {
	rows, total, err := e.txRepo.Paginate(filters)
	if err != nil {
		return nil, 0, fmt.Errorf("ramps: list orders: %w", err)
	}
	return e.toOrderViews(rows), total, nil
}

// show loads an order and answers it refreshed. Type mismatch reads as absent:
// an off-ramp id must not surface through the on-ramp endpoint dressed as one.
func (e *engine) show(ctx context.Context, customerID uint, externalID, txType string) (*OrderView, error) {
	tx, err := e.load(customerID, externalID, txType)
	if err != nil {
		return nil, err
	}
	e.refreshInFlight(ctx, tx)
	return e.toOrderView(tx), nil
}

// load reads the customer's order of txType, or ErrOrderNotFound.
func (e *engine) load(customerID uint, externalID, txType string) (*models.Transaction, error) {
	tx, err := e.txRepo.FindByCustomerAndExternalID(customerID, externalID)
	if err != nil {
		return nil, fmt.Errorf("ramps: load order: %w", err)
	}
	if tx == nil || tx.Type != txType {
		return nil, ErrOrderNotFound
	}
	return tx, nil
}

// refreshInFlight reconciles a non-terminal order against the provider before
// it is answered.
//
// Webhooks alone are not a status machine: a dropped or delayed delivery would
// otherwise leave the order sitting in `awaiting_funds` while the user stares
// at it. The read path is the second source of truth.
//
// A provider failure is logged and swallowed. The persisted row is still a
// correct answer — just an older one — and failing the read would turn a
// provider hiccup into a broken screen.
//
// A refresh that completes an on-ramp is the path that saw the pay-in settle,
// so it asks for the delivery: the event that lands afterwards finds the order
// finished (the app polls while the user pays, so this is the common case, not
// a race). One that lost the write to another writer leaves it to the winner.
func (e *engine) refreshInFlight(ctx context.Context, tx *models.Transaction) {
	if rampsvc.NeedsProviderRefresh(tx) {
		changed, err := e.refreshOzavTwoPhase(ctx, tx)
		if err != nil {
			facades.Log().WithContext(ctx).With(map[string]any{"order": tx.ExternalID}).Warningf("ramps: refresh order: %v", err)
			return
		}
		if changed {
			e.publish(tx)
			e.notifier.TransactionUpdated(tx)
			rampsvc.DeliverSettledOnramp(ctx, e, tx)
		}
		return
	}
	if models.IsTerminalStatus(tx.Status) {
		return
	}
	if tx.GatewayOrderID == "" {
		return
	}
	refresh := e.refreshTransferOrder
	switch {
	case tx.LiquidationAddressID != nil:
		refresh = e.refreshDrainOrder
	case tx.VirtualAccountID != nil:
		refresh = e.refreshDepositOrder
	}
	changed, err := refresh(ctx, tx)
	if err != nil {
		facades.Log().WithContext(ctx).With(map[string]any{"order": tx.ExternalID}).Warningf("ramps: refresh order: %v", err)
		return
	}
	if changed {
		e.publish(tx)
		// The provider moved the order, not the user: if the move ended it,
		// the devices are told too, as they would be had the event got here
		// first. One that did reaches the user once all the same.
		e.notifier.TransactionUpdated(tx)
		rampsvc.DeliverSettledOnramp(ctx, e, tx)
	}
}

func (e *engine) refreshOzavTwoPhase(ctx context.Context, tx *models.Transaction) (bool, error) {
	var changed bool
	if tx.GatewayOrderID != "" {
		c, err := e.refreshProviderTransfer(ctx, tx, tx.GatewayOrderID)
		if err != nil {
			return false, err
		}
		changed = changed || c
	}
	c, err := e.refreshDelivery(ctx, tx)
	if err != nil {
		return false, err
	}
	return changed || c, nil
}

// refreshDelivery reads the delivery transfer for its outcome only. Running it
// through ApplyOrder like the pay-in would write the delivery's id over the
// pay-in's and its token amount into amount_brl, and would drop a failure.
//
// It resolves for settlement, not eligibility: this is bookkeeping on money the
// user already paid, the same reason Deliver does.
func (e *engine) refreshDelivery(ctx context.Context, tx *models.Transaction) (bool, error) {
	if tx.DeliveryOrderID == nil || strings.TrimSpace(*tx.DeliveryOrderID) == "" || rampsvc.OnrampCredited(tx) {
		return false, nil
	}
	adapter, ref, err := e.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
	if err != nil {
		return false, err
	}
	saved, err := rampsvc.RefreshDelivery(ctx, adapter, ref, e.txRepo, tx, e.config.OzavDeliveryMaxAttempts())
	if err != nil {
		return false, err
	}
	if !saved {
		e.reload(tx)
	}
	return saved, nil
}

// isBankPayout reports whether the row is funded by a prepared action: an ACH
// or wire payout, or a Pix payout a provider prepared. The webhook,
// the reconciler and the recovery own those: they read the payout against the
// binding stored at finalize, which the generic GetOrder (unbound, unclamped)
// does not, so a client read leaves the order as stored.
func isBankPayout(tx *models.Transaction) bool {
	return tx.FundedByAuthorization()
}

func (e *engine) refreshProviderTransfer(ctx context.Context, tx *models.Transaction, providerOrderID string) (bool, error) {
	if isBankPayout(tx) {
		return false, nil
	}
	rail := ramp.RailPixPayin
	if tx.Rail != nil {
		rail = ramp.Rail(*tx.Rail)
	}
	adapter, ref, err := e.router.ResolveProvider(ctx, tx.CustomerID, rail, tx.Provider)
	if err != nil {
		return false, err
	}
	order, err := adapter.GetOrder(ctx, ref, providerOrderID)
	if err != nil {
		return false, err
	}
	previous := tx.Status
	if !rampsvc.OrderChangedFromProvider(tx, order) {
		return false, nil
	}
	rampsvc.ApplyOrder(tx, order)
	if rampsvc.ShouldSetCompletedAt(tx, tx.Status) && tx.CompletedAt == nil {
		now := time.Now()
		tx.CompletedAt = &now
	}
	return e.persistRefresh(tx, previous)
}

func (e *engine) refreshTransferOrder(ctx context.Context, tx *models.Transaction) (bool, error) {
	if isBankPayout(tx) {
		return false, nil
	}
	rail := ramp.RailPixPayin
	if tx.Rail != nil {
		rail = ramp.Rail(*tx.Rail)
	}
	adapter, ref, err := e.router.ResolveProvider(ctx, tx.CustomerID, rail, tx.Provider)
	if err != nil {
		return false, err
	}

	order, err := adapter.GetOrder(ctx, ref, tx.GatewayOrderID)
	if err != nil {
		return false, err
	}
	previous := tx.Status
	if !rampsvc.OrderChangedFromProvider(tx, order) {
		return false, nil
	}

	rampsvc.ApplyOrder(tx, order)
	if rampsvc.ShouldSetCompletedAt(tx, tx.Status) && tx.CompletedAt == nil {
		now := time.Now()
		tx.CompletedAt = &now
	}

	return e.persistRefresh(tx, previous)
}

func (e *engine) refreshDepositOrder(ctx context.Context, tx *models.Transaction) (bool, error) {
	account, err := e.vaRepo.FindByID(*tx.VirtualAccountID)
	if err != nil {
		return false, fmt.Errorf("ramps: load virtual account: %w", err)
	}
	if account == nil {
		return false, fmt.Errorf("ramps: order %s names missing virtual account %d",
			tx.ExternalID, *tx.VirtualAccountID)
	}

	standing, ref, err := rampsvc.ResolveDepositAdapter(e.factory, e.integRepo, tx)
	if err != nil {
		return false, err
	}
	deposit, err := standing.GetDeposit(ctx, ref, account.ExternalAccountID, tx.GatewayOrderID)
	if err != nil {
		return false, err
	}
	if deposit == nil {
		return false, nil
	}

	previous := tx.Status
	if !rampsvc.DepositChangedFromProvider(tx, account, deposit) {
		return false, nil
	}

	rampsvc.ApplyDeposit(tx, account, deposit)
	return e.persistRefresh(tx, previous)
}

// refreshDrainOrder re-reads a USD payout out of its address's drains, the way
// refreshDepositOrder re-reads a deposit, through the reconciler's own fold. A
// service built without the address repository leaves the order as stored.
func (e *engine) refreshDrainOrder(ctx context.Context, tx *models.Transaction) (bool, error) {
	if e.laRepo == nil {
		return false, nil
	}
	address, err := e.laRepo.FindByID(*tx.LiquidationAddressID)
	if err != nil {
		return false, fmt.Errorf("ramps: load liquidation address: %w", err)
	}
	if address == nil {
		return false, fmt.Errorf("ramps: order %s names missing liquidation address %d",
			tx.ExternalID, *tx.LiquidationAddressID)
	}

	payout, ref, err := rampsvc.ResolveDrainAdapter(e.factory, e.integRepo, tx)
	if err != nil {
		return false, err
	}
	drain, err := payout.GetDrain(ctx, ref, address.ExternalAddressID, tx.GatewayOrderID)
	if err != nil {
		return false, err
	}

	previous := tx.Status
	if !rampsvc.DrainChangedFromProvider(tx, address, drain) {
		return false, nil
	}
	// The webhook's and the reconciler's guard: a payout the bank returned
	// must not be walked back to completed by the drain's payment_processed.
	if !rampsvc.DrainMayMove(tx, drain) {
		return false, nil
	}
	rampsvc.ApplyLiquidationDrain(tx, address, drain)
	return e.persistRefresh(tx, previous)
}

func (e *engine) persistRefresh(tx *models.Transaction, previous string) (bool, error) {
	updated, err := e.txRepo.SaveProviderSnapshotIfStatus(tx, previous)
	if err != nil {
		return false, fmt.Errorf("ramps: persist refresh: %w", err)
	}
	if !updated {
		facades.Log().Debugf("ramps: refresh lost CAS race for %s", tx.ExternalID)
		e.reload(tx)
		return false, nil
	}
	return true, nil
}

// reload replaces tx with the stored row after a conditional write lost, so the
// answer is the winner's state rather than this call's discarded mutation.
func (e *engine) reload(tx *models.Transaction) {
	reloaded, err := e.txRepo.FindByCustomerAndExternalID(tx.CustomerID, tx.ExternalID)
	if err != nil {
		facades.Log().Warningf("ramps: reload order %s after lost CAS: %v", tx.ExternalID, err)
		return
	}
	if reloaded != nil {
		*tx = *reloaded
	}
}
