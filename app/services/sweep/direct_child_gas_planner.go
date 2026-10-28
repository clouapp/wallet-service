package sweep

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func (s *service) directChildGasPlan(ctx context.Context, wallet *models.Wallet, child models.Address, plan *Plan, to string) (*Plan, error) {
	entity, err := s.loadChain(ctx, wallet.Chain)
	if err != nil {
		return nil, err
	}
	if entity == nil || entity.NetworkID == nil || wallet.AccountID == nil || to == "" {
		return nil, ErrWalletNotGasReady
	}
	token, err := s.registry.FindToken(wallet.Chain, plan.Asset)
	if err != nil {
		return nil, err
	}
	b := DirectChildGasBinding{WalletID: wallet.ID, AccountID: *wallet.AccountID, SourceAddressID: child.ID, SourceAddress: child.Address,
		SourceDerivationIndex: child.DerivationIndex, SourceExternalUserID: child.ExternalUserID, Chain: wallet.Chain, NetworkID: *entity.NetworkID,
		Asset: plan.Asset, TokenContract: token.Contract, TokenDecimals: token.Decimals, AmountRaw: plan.Amount.String(), Recipient: to}
	quote, err := s.QuoteDirectChildGas(ctx, b)
	if err != nil {
		return nil, err
	}
	copyPlan := *plan
	copyChild := child
	copyPlan.Strategy, copyPlan.SourceAddress, copyPlan.ReachesTarget = StrategyDirectFromChild, &copyChild, true
	copyPlan.GasPreparation, copyPlan.GasPreparationBinding = quote, &b
	tokenFee, _ := gasPositive(quote.TokenFeeWei)
	seedFee, _ := gasPositive(quote.MaxFeeWei)
	copyPlan.EstimatedGas = new(big.Int).Add(tokenFee, seedFee)
	return &copyPlan, nil
}

// PlanForOperation reads an existing immutable preparation before the regular
// strategy selection. A retry cannot quietly move another child's funds.
func (s *service) PlanForOperation(ctx context.Context, operationID, walletID uuid.UUID, asset, amountRaw string, to Destination, caller uuid.UUID) (*Plan, error) {
	amount, err := gasPositive(amountRaw)
	if err != nil {
		return nil, err
	}
	if err = s.gate(ctx, caller); err != nil {
		return nil, err
	}
	wallet, err := s.walletRepo.FindByID(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if wallet == nil {
		return nil, ErrGasPreparationBinding
	}
	adapter, err := s.registry.ChainForWallet(wallet)
	if err != nil {
		return nil, err
	}
	if evmTokenGasApplies(adapter, asset) {
		store, storeErr := s.gasPreparationStore()
		if storeErr != nil {
			return nil, storeErr
		}
		row, findErr := store.FindByID(ctx, DirectChildGasStepID(operationID))
		if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
			return nil, findErr
		}
		if row != nil {
			var record directChildGasRecord
			if recordErr := readGasPreparationPayload(row, &record); recordErr != nil {
				return nil, recordErr
			}
			b := record.Binding
			if b.OperationID != operationID || b.WalletID != walletID || b.ClaimID != operationID || !strings.EqualFold(b.Asset, asset) || b.AmountRaw != amount.String() || !strings.EqualFold(b.Recipient, to.Address) || to.Tag != nil {
				return nil, ErrGasPreparationBinding
			}
			if _, recordErr := readDirectGasRecord(row, b); recordErr != nil {
				return nil, recordErr
			}
			g, contextErr := s.directGasContext(ctx, b)
			if contextErr != nil {
				return nil, contextErr
			}
			if err = s.gate(ctx, b.AccountID); err != nil {
				return nil, err
			}
			return &Plan{WalletID: walletID, Chain: b.Chain, Asset: asset, Amount: amount, Strategy: StrategyDirectFromChild, SourceAddress: &g.child,
				ReachesTarget: true, GasPreparation: &record.Plan, GasPreparationBinding: &b, gasPreparationExisting: true, WithdrawalID: operationID, IdempotencyKey: operationID.String()}, nil
		}
	}
	plan, err := s.PlanForWithdrawalTo(ctx, walletID, asset, amount, to, caller)
	if err != nil {
		return nil, err
	}
	plan.WithdrawalID, plan.IdempotencyKey = operationID, operationID.String()
	if plan.GasPreparationBinding != nil {
		plan.GasPreparationBinding.OperationID, plan.GasPreparationBinding.ClaimID = operationID, operationID
	}
	return plan, nil
}

func readGasPreparationPayload(row *models.Transaction, record *directChildGasRecord) error {
	var payload struct {
		Record directChildGasRecord `json:"direct_child_gas"`
	}
	if err := json.Unmarshal([]byte(row.RawPayload), &payload); err != nil {
		return ErrGasPreparationBinding
	}
	*record = payload.Record
	return nil
}

var _ OperationPlanner = (*service)(nil)
var _ DirectChildGasPreparer = (*service)(nil)
