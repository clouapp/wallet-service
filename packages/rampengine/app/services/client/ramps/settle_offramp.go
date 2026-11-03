package ramps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// settleLockKey serializes settlement per customer rather than per order: every
// order of the customer draws on the same managed wallet, and two settling at once
// would each see the balance the other is about to spend.
func settleLockKey(customerID uint) string {
	return fmt.Sprintf("offramp-settle:customer:%d", customerID)
}

// needsOfframpSettlement reports an Ozav off-ramp waiting for payout after funding.
func needsOfframpSettlement(tx *models.Transaction) bool {
	if tx == nil || tx.Type != models.TransactionTypeOfframp {
		return false
	}
	if tx.Provider != models.ProviderOzav {
		return false
	}
	if tx.GatewayOrderID != "" {
		return false
	}
	switch tx.Status {
	case models.TxStatusAwaitingFunds, models.TxStatusFundsReceived:
		return true
	default:
		return false
	}
}

// Settle executes the payout leg for a funded Ozav off-ramp.
func (s *OfframpService) Settle(ctx context.Context, txID uint) error {
	owner, err := s.txRepo.FindByID(txID)
	if err != nil {
		return fmt.Errorf("ramps: load offramp %d: %w", txID, err)
	}
	if owner == nil {
		// Retrying cannot make a missing row appear.
		facades.Log().WithContext(ctx).Warningf("ramps: settle offramp %d: not found", txID)
		return nil
	}
	return s.locker.WithLock(ctx, settleLockKey(owner.CustomerID), func(ctx context.Context) error {
		// Re-read under the lock: another settle may have paid it out while
		// this one waited.
		tx, err := s.txRepo.FindByID(txID)
		if err != nil {
			return fmt.Errorf("ramps: load offramp %d: %w", txID, err)
		}
		if tx == nil || !needsOfframpSettlement(tx) {
			return nil
		}
		return s.settleLocked(ctx, tx)
	})
}

func (s *OfframpService) settleLocked(ctx context.Context, tx *models.Transaction) error {
	adapter, ref, err := s.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
	if err != nil {
		return fmt.Errorf("ramps: resolve provider to settle offramp %s: %w", tx.ExternalID, err)
	}
	settler, ok := adapter.(ramp.OfframpSettlementAdapter)
	if !ok {
		return fmt.Errorf("ramps: provider %s does not implement off-ramp settlement", tx.Provider)
	}
	// An order a deposit claimed by hash settles on that claim. One without
	// is funded only by the balance, and the check that queued it ran outside
	// the lock: another order may have spent that balance since.
	if !depositClaimed(tx) {
		funded, err := s.offrampFundingSatisfied(ctx, tx)
		if err != nil {
			return fmt.Errorf("ramps: recheck offramp funding %s: %w", tx.ExternalID, err)
		}
		if !funded {
			return nil
		}
	}
	req, err := s.settleRequest(tx)
	if err != nil {
		return err
	}
	order, err := settler.SettleOfframp(ctx, ref, req)
	if errors.Is(err, ramp.ErrEstimateExpired) {
		return s.failRefusedSettlement(ctx, tx, "quote_expired_settlement", ramp.ErrQuoteExpiredSettlement,
			"ramps: offramp quote expired before payout — the user's crypto is still in the managed wallet")
	}
	// The provider quoted the amount and then refused the payout under its own
	// floor. The same request under the same key gets the same answer, so
	// leaving the order awaiting only re-posts it on every poll.
	if errors.Is(err, ramp.ErrAmountBelowMinimum) {
		return s.failRefusedSettlement(ctx, tx, "amount_below_minimum", err,
			"ramps: provider refused the offramp payout as below its minimum — the user's crypto is still in the managed wallet")
	}
	if err != nil {
		return fmt.Errorf("ramps: settle offramp %s: %w", tx.ExternalID, err)
	}
	applyCreatedOrder(tx, order)
	if order.ProviderDestinationRef != "" {
		tx.ProviderDestinationRef = &order.ProviderDestinationRef
	}
	if err := s.txRepo.Update(tx); err != nil {
		return fmt.Errorf("ramps: persist offramp settlement %s: %w", tx.ExternalID, err)
	}
	s.publish(tx)
	return nil
}

// settleRequest is the payout the order was quoted for, against the provider
// quote and the destination recorded when it was placed.
func (s *OfframpService) settleRequest(tx *models.Transaction) (ramp.OfframpSettleRequest, error) {
	if tx.IdempotencyKey == nil || *tx.IdempotencyKey == "" {
		return ramp.OfframpSettleRequest{}, fmt.Errorf("ramps: offramp %s has no idempotency key", tx.ExternalID)
	}
	destinationRef := deref(tx.ProviderDestinationRef)
	if destinationRef == "" {
		return ramp.OfframpSettleRequest{}, fmt.Errorf("ramps: offramp %s has no payout destination ref", tx.ExternalID)
	}
	estimateRow, snapshot, err := s.loadEstimateForSettlement(tx.CustomerID, tx.EstimateID)
	if err != nil {
		return ramp.OfframpSettleRequest{}, fmt.Errorf("ramps: load estimate for offramp %s: %w", tx.ExternalID, err)
	}
	req := ramp.OfframpSettleRequest{
		OfframpCreateRequest: ramp.OfframpCreateRequest{
			OfframpQuoteRequest: offrampQuoteRequest(dtoFromOfframpSnapshot(snapshot), deref(tx.FeePercent)),
			ProviderQuoteID:     deref(estimateRow.ProviderQuoteID),
			IdempotencyKey:      *tx.IdempotencyKey,
		},
		ExternalAccountID: destinationRef,
	}
	req.Source.Amount = tx.AmountToken
	return req, nil
}

// failRefusedSettlement closes an order the provider will not pay out — its
// quote expired, or it refused the amount. The user's crypto is still in the
// managed wallet, so this is logged as an error for an operator to return or
// re-price it: nothing here moves it.
func (s *OfframpService) failRefusedSettlement(ctx context.Context, tx *models.Transaction, reason string, cause error, message string) error {
	tx.Status = models.TxStatusFailed
	tx.FailureReason = &reason
	if err := s.txRepo.Update(tx); err != nil {
		return fmt.Errorf("ramps: persist %s %s: %w", reason, tx.ExternalID, err)
	}
	facades.Log().WithContext(ctx).With(map[string]any{"order": tx.ExternalID, "funded": tx.DepositTxSignature != nil}).
		Error(message)
	s.publish(tx)
	return fmt.Errorf("ramps: settle offramp %s: %w", tx.ExternalID, cause)
}

func (s *OfframpService) loadEstimateForSettlement(customerID uint, estimateID *uint) (*models.RampEstimate, *estimateSnapshot, error) {
	if estimateID == nil {
		return nil, nil, ramp.ErrEstimateRequired
	}
	row, err := s.estimateRepo.FindByCustomerAndID(customerID, *estimateID)
	if err != nil {
		return nil, nil, err
	}
	if row == nil {
		return nil, nil, ErrEstimateNotFound
	}
	var snapshot estimateSnapshot
	if err := json.Unmarshal([]byte(row.RawResponse), &snapshot); err != nil {
		return nil, nil, fmt.Errorf("ramps: decode estimate snapshot: %w", err)
	}
	return row, &snapshot, nil
}

func dtoFromOfframpSnapshot(snapshot *estimateSnapshot) dto.EstimateOfframpDTO {
	if snapshot == nil {
		return dto.EstimateOfframpDTO{}
	}
	return dto.EstimateOfframpDTO{
		AmountToken:       snapshot.SourceAmount,
		Token:             snapshot.SourceCurrency,
		Chain:             snapshot.Chain,
		FromAddress:       snapshot.FromAddress,
		PixKey:            snapshot.PixKey,
		PixKeyType:        snapshot.PixKeyType,
		RecipientName:     snapshot.RecipientName,
		RecipientDocument: snapshot.RecipientDocument,
	}
}
