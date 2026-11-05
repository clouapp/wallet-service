package ramps

import (
	"context"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const offrampDepositMatchScale = 6

// ApplyManagedWalletDeposit claims a deposit.credited for the oldest awaiting
// Ozav off-ramp it matches and returns the settle enqueue for the caller to run
// once its own transaction has committed. The webhook is authoritative over
// balance polling when Ozav's /balances aggregate lags the panel.
//
// The claim is its own transaction, keyed by the deposit's hash: an order a
// deposit already claimed is never claimed by a second one, and a redelivered
// or reprocessed event finds its own claim and enqueues again, which Settle
// absorbs. A deposit without a hash cannot be claimed by anything, so it hands
// the user's open orders to the balance check, which allocates oldest first.
func (s *OfframpService) ApplyManagedWalletDeposit(
	ctx context.Context, customerID uint, dep ramp.ManagedWalletDeposit,
) (func(), error) {
	if dep.WalletID == "" || dep.Amount == "" {
		return nil, nil
	}
	if strings.TrimSpace(dep.TxHash) == "" {
		return func() { s.enqueueFundedOfframps(ctx, customerID) }, nil
	}
	tolerance, err := s.depositMatchTolerance()
	if err != nil {
		return nil, err
	}
	matched, err := s.txRepo.ClaimOfframpDeposit(customerID, models.ProviderOzav, dep.TxHash,
		func(tx *models.Transaction) bool { return offrampMatchesDeposit(tx, dep, tolerance) })
	if err != nil {
		return nil, fmt.Errorf("ramps: claim offramp deposit: %w", err)
	}
	if matched == nil {
		facades.Log().WithContext(ctx).With(map[string]any{
			"customer_id": customerID, "wallet_id": dep.WalletID, "amount": dep.Amount,
			"currency": dep.Currency, "network": dep.Network,
		}).Warning("ramps: deposit.credited matched no awaiting off-ramp")
		return nil, nil
	}
	return func() {
		if err := s.enqueueSettle(ctx, matched); err != nil {
			facades.Log().WithContext(ctx).With(map[string]any{"order": matched.ExternalID}).
				Errorf("ramps: enqueue settle for claimed deposit: %v", err)
		}
	}, nil
}

// depositMatchTolerance reads the pix_payout tolerance. An unreadable rail is
// an error, not a silent exact match.
func (s *OfframpService) depositMatchTolerance() (string, error) {
	if s.config == nil {
		return "0", nil
	}
	policy, err := s.config.Rail(ramp.RailPixPayout)
	if err != nil {
		return "", fmt.Errorf("ramps: read deposit match tolerance: %w", err)
	}
	return policy.OzavDepositMatchTolerance, nil
}

// enqueueFundedOfframps runs the balance check over every open Ozav off-ramp
// of the user, oldest first.
func (s *OfframpService) enqueueFundedOfframps(ctx context.Context, customerID uint) {
	open, err := s.txRepo.FindOpenOfframpsAwaitingSettlement(customerID, models.ProviderOzav)
	if err != nil {
		facades.Log().WithContext(ctx).Errorf("ramps: list offramps for unhashed deposit: %v", err)
		return
	}
	for i := range open {
		if _, err := s.EnqueueSettleIfFunded(ctx, &open[i]); err != nil {
			facades.Log().WithContext(ctx).With(map[string]any{"order": open[i].ExternalID}).
				Warningf("ramps: offramp settle check: %v", err)
		}
	}
}

func offrampMatchesDeposit(tx *models.Transaction, dep ramp.ManagedWalletDeposit, tolerance string) bool {
	if !sameOfframpAsset(tx, dep.Currency, dep.Network) {
		return false
	}
	return ramp.AmountCoversWithinTolerance(strings.TrimSpace(dep.Amount), strings.TrimSpace(tx.AmountToken),
		tolerance, offrampDepositMatchScale)
}

// sameOfframpAsset reports whether a token and network name what tx moves. An
// empty network on the other side is not a mismatch: not every source says.
func sameOfframpAsset(tx *models.Transaction, currency, network string) bool {
	if tx.TargetToken != "" && !strings.EqualFold(tx.TargetToken, currency) {
		return false
	}
	if tx.Chain != nil {
		want := strings.TrimSpace(network)
		if want != "" && !strings.EqualFold(*tx.Chain, want) {
			return false
		}
	}
	return true
}
