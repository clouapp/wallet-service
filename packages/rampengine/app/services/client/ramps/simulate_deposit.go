package ramps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const simulatedDepositPrefix = "sandbox-simulated:"

// SimulateDeposit completes one of the customer's off-ramps locally when
// deposit mode is simulated. No on-chain transfer or Bridge webhook is involved.
func (s *OfframpService) SimulateDeposit(ctx context.Context, customerID uint, externalID string) (*OrderView, error) {
	tx, err := s.txRepo.FindByCustomerAndExternalID(customerID, externalID)
	if err != nil {
		return nil, fmt.Errorf("ramps: load offramp: %w", err)
	}
	if tx == nil || tx.Type != models.TransactionTypeOfframp {
		return nil, ErrOrderNotFound
	}

	if s.appEnv == "production" {
		return nil, ramp.ErrSandboxOnly
	}
	mode, err := s.depositMode(tx)
	if err != nil {
		return nil, err
	}
	if mode != DepositModeSimulated {
		return nil, ramp.ErrSandboxOnly
	}

	if tx.Status == models.TxStatusCompleted &&
		tx.DepositTxSignature != nil &&
		strings.HasPrefix(*tx.DepositTxSignature, simulatedDepositPrefix) {
		return s.toOrderView(tx), nil
	}
	if tx.Status != models.TxStatusAwaitingFunds {
		return nil, fmt.Errorf("ramps: order not awaiting funds: %w", ErrInvalidOrderState)
	}

	sig := simulatedDepositPrefix + uuid.NewString()
	tx.DepositTxSignature = &sig
	if err := s.txRepo.Update(tx); err != nil {
		return nil, fmt.Errorf("ramps: persist simulated deposit: %w", err)
	}

	if tx.Provider == models.ProviderOzav {
		if err := s.Settle(ctx, tx.ID); err != nil {
			return nil, fmt.Errorf("ramps: settle simulated offramp: %w", err)
		}
		reloaded, err := s.txRepo.FindByCustomerAndExternalID(customerID, externalID)
		if err != nil {
			return nil, fmt.Errorf("ramps: reload offramp after settle: %w", err)
		}
		if reloaded != nil {
			tx = reloaded
		}
		s.publish(tx)
		return s.toOrderView(tx), nil
	}

	now := time.Now()
	tx.Status = models.TxStatusCompleted
	tx.CompletedAt = &now
	if err := s.txRepo.Update(tx); err != nil {
		return nil, fmt.Errorf("ramps: persist simulated deposit: %w", err)
	}
	s.publish(tx)
	return s.toOrderView(tx), nil
}
