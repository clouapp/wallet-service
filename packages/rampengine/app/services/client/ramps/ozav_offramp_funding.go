package ramps

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	contractsqueue "github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// offrampSettleCheckTTL throttles the read path's balance check per order:
// the app polls an awaiting order, and each poll would otherwise be a call to
// the provider's balances endpoint.
const (
	offrampSettleCheckPrefix = "offramp-settle-check:"
	offrampSettleCheckTTL    = 30 * time.Second
)

// settleIfFundedThrottled is the read path's balance check: at most one per
// order per offrampSettleCheckTTL.
func (s *OfframpService) settleIfFundedThrottled(ctx context.Context, tx *models.Transaction) error {
	if !needsOfframpSettlement(tx) {
		return nil
	}
	key := offrampSettleCheckPrefix + strconv.FormatUint(uint64(tx.ID), 10)
	if !facades.Cache().Add(key, time.Now().Unix(), offrampSettleCheckTTL) {
		return nil
	}
	_, err := s.EnqueueSettleIfFunded(ctx, tx)
	return err
}

// EnqueueSettleIfFunded queues settlement when the managed wallet covers the
// order, and reports whether it did.
func (s *OfframpService) EnqueueSettleIfFunded(ctx context.Context, tx *models.Transaction) (bool, error) {
	if !needsOfframpSettlement(tx) {
		return false, nil
	}
	funded, err := s.offrampFundingSatisfied(ctx, tx)
	if err != nil || !funded {
		return false, err
	}
	return true, s.enqueueSettle(ctx, tx)
}

func (s *OfframpService) enqueueSettle(ctx context.Context, tx *models.Transaction) error {
	if s.settleJob == nil {
		return s.Settle(ctx, tx.ID)
	}
	pending, err := s.settleJob.Stage(
		contractsqueue.Arg{Value: uint64(tx.ID), Type: "uint64"},
	)
	if err != nil {
		return fmt.Errorf("ramps: stage settle offramp job: %w", err)
	}
	return pending.Dispatch()
}

func (s *OfframpService) offrampFundingSatisfied(ctx context.Context, tx *models.Transaction) (bool, error) {
	adapter, ref, err := s.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
	if err != nil {
		return false, fmt.Errorf("ramps: resolve provider for offramp funding %s: %w", tx.ExternalID, err)
	}
	settler, ok := adapter.(ramp.OfframpSettlementAdapter)
	if !ok {
		return false, nil
	}
	_, snapshot, err := s.loadEstimateForSettlement(tx.CustomerID, tx.EstimateID)
	if err != nil {
		return false, err
	}
	required, err := s.fundingRequiredThrough(tx)
	if err != nil {
		return false, err
	}
	in := dtoFromOfframpSnapshot(snapshot)
	in.AmountToken = required
	req := ramp.OfframpFundingRequest{
		OfframpCreateRequest: ramp.OfframpCreateRequest{
			OfframpQuoteRequest: offrampQuoteRequest(in, deref(tx.FeePercent)),
		},
	}
	req.Source.Amount = required
	return settler.OfframpFundingSatisfied(ctx, ref, req)
}

// fundingRequiredThrough is what the managed wallet has to hold before tx
// counts as funded. The wallet is one per customer, so its balance cannot say
// which order a deposit was for: older open off-ramps in the same token and
// network are funded first, and on-ramp tokens waiting there for delivery are
// the user's but not for this. Without this, one deposit — or an undelivered
// on-ramp — read as funding every awaiting order at once.
//
// A newer order a deposit already claimed by hash counts too: that deposit is
// its, and without it an older order would be paid out of a newer one's money.
func (s *OfframpService) fundingRequiredThrough(tx *models.Transaction) (string, error) {
	amounts := []string{tx.AmountToken}
	open, err := s.txRepo.FindOpenOfframpsAwaitingSettlement(tx.CustomerID, tx.Provider)
	if err != nil {
		return "", fmt.Errorf("ramps: list open offramps for funding: %w", err)
	}
	older := true
	for i := range open {
		other := &open[i]
		if other.ID == tx.ID {
			older = false
			continue
		}
		if !sameOfframpAsset(other, tx.TargetToken, deref(tx.Chain)) {
			continue
		}
		if older || depositClaimed(other) {
			amounts = append(amounts, other.AmountToken)
		}
	}
	held, err := s.txRepo.FindOnrampsInProviderCustody(tx.CustomerID, tx.Provider)
	if err != nil {
		return "", fmt.Errorf("ramps: list onramps in custody for funding: %w", err)
	}
	for i := range held {
		if sameOfframpAsset(&held[i], tx.TargetToken, deref(tx.Chain)) {
			amounts = append(amounts, held[i].AmountToken)
		}
	}
	return ramp.SumAmounts(offrampDepositMatchScale, amounts...)
}

// depositClaimed reports whether a deposit.credited has claimed tx by hash.
func depositClaimed(tx *models.Transaction) bool {
	return strings.TrimSpace(deref(tx.DepositTxSignature)) != ""
}
