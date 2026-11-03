package ramps

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// createPreparedPix places a Pix payout whose quote came from a provider that
// prepares the funding, and answers it with the funding action the wallet
// signs. Create has already run the gates the two shapes share (the rail, the
// quote, its limits in reais, the holder and the bind); what is left is the
// bank rails' own path: the provider only prepares, which moves no money, so
// any failure frees the reservation and leaves the quote redeemable.
func (s *OfframpService) createPreparedPix(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.CreateOfframpDTO,
	estimate *models.RampEstimate, snapshot *estimateSnapshot, fee string,
) (*OrderView, bool, error) {
	// A service built without the port cannot place it; saying so beats a nil
	// dereference further down.
	if s.funding == nil {
		return nil, false, fmt.Errorf("ramps: prepared payout create not wired: %w", ramp.ErrOfframpCreateUnavailable)
	}
	if early, err := s.dedupByEstimate(customer.ID, estimate, models.TransactionTypeOfframp); err != nil {
		return nil, false, err
	} else if early != nil && !stale(early) {
		return s.toOrderView(early), false, nil
	}

	c := &preparedCreate{
		rail: ramp.RailPixPayout, estimate: estimate, snapshot: snapshot, fee: fee,
		build: newPreparedPixTransaction, gates: s.businessGates,
	}
	reserved, err := s.reservePrepared(ctx, user, customer, in, c)
	if err != nil {
		return nil, false, err
	}
	if reserved.existing != nil {
		return s.toOrderView(reserved.existing), false, nil
	}
	if err := s.prepareFunding(ctx, reserved, c); err != nil {
		return nil, false, err
	}
	s.publish(reserved.tx)
	return s.toOrderView(reserved.tx), true, nil
}

// replayConsumedPix answers a create naming a quote an order already consumed:
// with that order when the quote was a prepared one, and as before
// (estimate_consumed) when it was not.
func (s *OfframpService) replayConsumedPix(
	customer *models.Customer, in dto.CreateOfframpDTO, row *models.RampEstimate, snap *estimateSnapshot,
) (*OrderView, error) {
	if snap.FundingMode != models.FundingModePrepared {
		return nil, ramp.ErrEstimateConsumed
	}
	holder, err := s.rules.holder(customer, in.RecipientName)
	if err != nil {
		return nil, err
	}
	in.RecipientName = holder
	if err := bindOfframpCreate(in, snap); err != nil {
		return nil, err
	}
	tx, err := s.dedupByEstimate(customer.ID, row, models.TransactionTypeOfframp)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, ramp.ErrEstimateConsumed
	}
	return s.toOrderView(tx), nil
}

// newPreparedPixTransaction builds the row a prepared Pix create reserves,
// from the quote it is bound to. The BRL leg is real here, unlike a bank
// payout's: it is what the quote said the recipient receives. It carries no
// bank field, and its funding is by authorization.
func newPreparedPixTransaction(customerID, userID uint, provider ramp.ProviderCode, c *preparedCreate) *models.Transaction {
	snap := c.snapshot
	tx := newTransaction(customerID, userID, provider, models.TransactionTypeOfframp, ramp.RailPixPayout)
	tx.AmountFiat, tx.FiatCurrency = snap.EstimatedReceive, models.FiatCurrencyBRL
	tx.AmountToken = snap.SourceAmount
	tx.TargetToken = snap.SourceCurrency
	from, chain := snap.FromAddress, snap.Chain
	tx.FromAddress, tx.Chain = &from, &chain
	pixKey, pixKeyType := snap.PixKey, snap.PixKeyType
	tx.PixKey, tx.PixKeyType = &pixKey, &pixKeyType
	if snap.RecipientName != "" {
		name := snap.RecipientName
		tx.RecipientName = &name
	}
	if snap.RecipientDocument != "" {
		document := snap.RecipientDocument
		tx.RecipientDocument = &document
	}
	fee := c.fee
	tx.FeePercent = &fee
	estimateID := c.estimate.ID
	tx.EstimateID = &estimateID
	funding, mode := models.FundingModePrepared, DepositModePrepared
	tx.FundingMode, tx.DepositMode = &funding, &mode
	return tx
}
