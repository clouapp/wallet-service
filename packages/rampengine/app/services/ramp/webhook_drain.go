package ramp

import (
	"cmp"
	"context"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// applyLiquidationDrain creates or moves the payout a drain describes.
//
// Like a virtual account deposit, the first report is the first XIP hears of
// the payout: the user sent crypto to a standing address, and no client
// create preceded it. Everything is read from result.Drain.
func (p *WebhookProcessor) applyLiquidationDrain(
	ctx context.Context,
	db repositories.WebhookTx,
	event *models.WebhookEvent,
	result *ramp.WebhookResult,
) (func(), error) {
	drain := result.Drain
	if drain == nil || drain.DrainID == "" {
		// Authentic, but with nothing to key a row on.
		return nil, nil
	}

	address, err := p.liquidationAddress(event.Provider, cmp.Or(result.ProviderAccountID, drain.ProviderAddressID))
	if err != nil {
		return nil, err
	}

	tx, err := db.FindDepositForUpdate(event.Provider, drain.DrainID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: load drain transaction: %w", err)
	}
	if tx == nil {
		return p.createDrainTransaction(ctx, db, event, address, drain)
	}

	// Out of order and duplicated delivery is normal; the terminal guard keeps
	// a late report from walking a settled payout back, except a bank's return
	// (DrainCanLeaveTerminal).
	if !DrainMayMove(tx, drain) {
		return nil, nil
	}

	ApplyLiquidationDrain(tx, address, drain)
	if err := db.UpdateTransaction(tx); err != nil {
		return nil, fmt.Errorf("ramp webhook: persist drain transaction: %w", err)
	}
	event.TransactionID = &tx.ID
	return func() { p.announce(tx) }, nil
}

// createDrainTransaction turns the first report of a drain into the payout,
// unless an operator closed the address's rail for the user. Then the event is
// accepted and nothing is opened, for the reason createDepositTransaction
// gives.
//
// A drain on an inactive address — its bank account or its user was deleted —
// is an error instead: Bridge may have paid an account the user removed, so it
// must not become an ordinary payout. The event stays in processing_error for
// an operator, as an unknown address does.
func (p *WebhookProcessor) createDrainTransaction(
	ctx context.Context,
	db repositories.WebhookTx,
	event *models.WebhookEvent,
	address *models.UserLiquidationAddress,
	drain *ramp.LiquidationDrain,
) (func(), error) {
	if address.Status != models.LiquidationAddressStatusActive {
		return nil, fmt.Errorf("ramp webhook: %s drain %s on %s liquidation address %d — held for review",
			event.Provider, drain.DrainID, address.Status, address.ID)
	}
	rail := ramp.Rail(address.Rail)
	on, err := p.features.IsActive(ctx, rail.Flag(), models.UserFlagScope(address.UserID))
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: %s switch for user %d: %w", rail, address.UserID, err)
	}
	if !on {
		facades.Log().Warningf(
			"ramp webhook: payout rail %s closed for user %d — drain %s recorded, no transaction opened",
			rail, address.UserID, drain.DrainID)
		return nil, nil
	}

	tx := &models.Transaction{}
	ApplyLiquidationDrain(tx, address, drain)
	if err := db.CreateTransaction(tx); err != nil {
		// A concurrent duplicate delivery loses this insert to
		// uniq_tx_provider_deposit: the designed path, as for a deposit.
		return nil, fmt.Errorf("ramp webhook: create drain transaction: %w", err)
	}
	event.TransactionID = &tx.ID
	return func() { p.announce(tx) }, nil
}

// liquidationAddress resolves the address a drain names, which is how the
// drain finds its user. A miss is an error: the event stays in
// processing_error for an operator.
func (p *WebhookProcessor) liquidationAddress(provider, externalAddressID string) (*models.UserLiquidationAddress, error) {
	if externalAddressID == "" {
		return nil, fmt.Errorf("ramp webhook: %s drain names no liquidation address", provider)
	}
	address, err := p.addresses.FindByProviderAndExternalID(provider, externalAddressID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: load liquidation address: %w", err)
	}
	if address == nil {
		return nil, fmt.Errorf("ramp webhook: no %s liquidation address %s", provider, externalAddressID)
	}
	return address, nil
}
