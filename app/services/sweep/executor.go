package sweep

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

// ExecutePlan runs a Plan against the live chain, co-signing with the provided
// ShareA (already decrypted by the caller from the wallet passphrase). It
// persists transaction rows with parent/origin tagging, emits webhooks per
// sweep leg, and reports partial failures so callers can retry idempotently
// from where the plan stopped.
func (s *service) ExecutePlan(
	ctx context.Context,
	plan *Plan,
	shareA []byte,
	withdrawalTxID uuid.UUID,
	toAddress string,
	externalUserID string,
) (*Result, error) {
	if plan == nil {
		return nil, fmt.Errorf("sweep: plan must not be nil")
	}
	if plan.Strategy == StrategyInsufficient {
		return nil, ErrInsufficientFunds
	}
	if toAddress == "" {
		return nil, fmt.Errorf("sweep: toAddress must not be empty")
	}

	wallet, err := s.walletRepo.FindByID(plan.WalletID)
	if err != nil {
		return nil, fmt.Errorf("sweep: find wallet: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("sweep: wallet %s not found", plan.WalletID)
	}
	if wallet.DepositAddress == nil {
		return nil, fmt.Errorf("sweep: wallet %s has no deposit address", plan.WalletID)
	}

	adapter, err := s.registry.Chain(plan.Chain)
	if err != nil {
		return nil, fmt.Errorf("sweep: adapter for %q: %w", plan.Chain, err)
	}

	shareB, err := s.fetchShareB(ctx, wallet)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(shareB)

	curve := mpcpkg.Curve(wallet.MPCCurve)
	result := &Result{WithdrawalTxID: withdrawalTxID}

	switch plan.Strategy {
	case StrategyDirectFromBase, StrategyDirectFromChild:
		if plan.SourceAddress == nil {
			return nil, fmt.Errorf("sweep: plan source address missing for strategy %s", plan.Strategy)
		}
		finalTx, err := s.broadcastWithdrawal(
			ctx, adapter, curve, shareA, shareB, wallet, plan,
			*plan.SourceAddress, toAddress, externalUserID, withdrawalTxID,
		)
		if err != nil {
			return nil, err
		}
		result.FinalWithdrawTx = finalTx
		return result, nil

	case StrategyMultiSweep:
		for i, leg := range plan.Sweeps {
			sweepTxID := uuid.New()
			hash, err := s.broadcastLeg(
				ctx, adapter, curve, shareA, shareB, wallet, plan,
				leg, sweepTxID, legBroadcastOpts{
					Origin:              models.TxOriginSweep,
					ParentTransactionID: &withdrawalTxID,
				},
			)
			if err != nil {
				slog.Error(
					"sweep leg failed",
					"index", i,
					"child", leg.From.Address,
					"wallet_id", wallet.ID,
					"error", err,
				)
				result.FailedStep = &FailedStep{
					Index:      i,
					LastError:  err.Error(),
					RetryReady: true,
				}
				return result, nil
			}
			result.Sweeps = append(result.Sweeps, CompletedSweep{
				From:         leg.From,
				TxHash:       hash,
				InternalTxID: sweepTxID,
			})
		}

		finalTx, err := s.broadcastWithdrawal(
			ctx, adapter, curve, shareA, shareB, wallet, plan,
			*wallet.DepositAddress, toAddress, externalUserID, withdrawalTxID,
		)
		if err != nil {
			return result, err
		}
		result.FinalWithdrawTx = finalTx
		return result, nil

	default:
		return nil, fmt.Errorf("sweep: unsupported strategy: %s", plan.Strategy)
	}
}

// legBroadcastOpts carries the per-call-site differences between the
// withdrawal-driven sweep path (ExecutePlan → multi_sweep) and the manual
// consolidate path (ConsolidateAll). The body of broadcastLeg is
// otherwise identical for both: same signing, broadcasting, and row
// schema; only the origin tag and the optional parent pointer differ.
type legBroadcastOpts struct {
	// Origin labels the sweep row (not the gas_seed row, which is always
	// tagged TxOriginGasSeed). Use TxOriginSweep for withdrawal-driven
	// sweeps and TxOriginManualConsolidation for the manual flow.
	Origin string
	// ParentTransactionID links the sweep row to the parent withdrawal
	// when set. Manual consolidation has no parent and passes nil.
	ParentTransactionID *uuid.UUID
}

// broadcastLeg builds, signs, broadcasts, and persists the transactions
// required to sweep `leg.Amount` of `plan.Asset` from `leg.From` to the
// wallet's base deposit address. On EVM with ERC-20, this produces two
// on-chain txs: an optional gas_seed (base → child native) followed by
// the sweep (child → base token transfer). On the single-tx path, only
// the sweep is produced. The sweep row inherits opts.Origin; the
// gas_seed row is always tagged TxOriginGasSeed. Both rows share
// opts.ParentTransactionID (so gas_seeds are traceable back to the
// withdrawal that triggered them in the executor path, and remain NULL
// in the manual consolidation path). Only the sweep row triggers a
// webhook event.
//
// Returns the on-chain hash of the sweep (not the gas_seed).
func (s *service) broadcastLeg(
	ctx context.Context,
	adapter types.Chain,
	curve mpcpkg.Curve,
	shareA, shareB []byte,
	wallet *models.Wallet,
	plan *Plan,
	leg PlannedSweep,
	sweepTxID uuid.UUID,
	opts legBroadcastOpts,
) (string, error) {
	nativeBal, err := adapter.GetBalance(ctx, leg.From.Address)
	if err != nil {
		return "", fmt.Errorf("native balance for %s: %w", leg.From.Address, err)
	}

	var token *types.Token
	if plan.Asset != adapter.NativeAsset() {
		t, ferr := s.registry.FindToken(plan.Chain, plan.Asset)
		if ferr != nil {
			return "", fmt.Errorf("find token %q: %w", plan.Asset, ferr)
		}
		token = t
	}

	unsigneds, err := adapter.BuildSweep(ctx, types.SweepRequest{
		From:          leg.From.Address,
		To:            wallet.DepositAddress.Address,
		Asset:         plan.Asset,
		Amount:        leg.Amount,
		NativeBalance: nativeBal.Amount,
		Token:         token,
	})
	if err != nil {
		return "", fmt.Errorf("build sweep: %w", err)
	}
	if len(unsigneds) == 0 {
		return "", fmt.Errorf("adapter returned no txs for sweep leg")
	}

	hasGasSeed := len(unsigneds) > 1
	var finalSweepHash string

	for idx := range unsigneds {
		unsigned := unsigneds[idx]

		sig, sigErr := s.mpc.Sign(ctx, curve, shareA, shareB, mpcpkg.SignInputs{
			TxHashes: [][]byte{unsigned.RawBytes},
		})
		if sigErr != nil {
			return "", fmt.Errorf("mpc sign: %w", sigErr)
		}

		signed := &types.SignedTx{ChainID: unsigned.ChainID, RawBytes: sig}
		hash, bcErr := adapter.BroadcastTransaction(ctx, signed)
		if bcErr != nil {
			return "", fmt.Errorf("broadcast: %w", bcErr)
		}

		isGasSeed := hasGasSeed && idx == 0

		origin := opts.Origin
		txType := models.TxTypeSweep
		txID := sweepTxID
		fromAddrStr := leg.From.Address
		toAddrStr := wallet.DepositAddress.Address
		childID := leg.From.ID
		addressID := &childID

		if isGasSeed {
			origin = models.TxOriginGasSeed
			txType = models.TxTypeGasSeed
			txID = uuid.New()
			fromAddrStr = wallet.DepositAddress.Address
			toAddrStr = leg.From.Address
			baseID := wallet.DepositAddress.ID
			addressID = &baseID
		}

		tx := &models.Transaction{
			ID:                  txID,
			WalletID:            wallet.ID,
			AddressID:           addressID,
			ExternalUserID:      leg.From.ExternalUserID,
			Chain:               plan.Chain,
			TxType:              txType,
			TxHash:              hash,
			FromAddress:         fromAddrStr,
			ToAddress:           toAddrStr,
			Amount:              leg.Amount.String(),
			Asset:               plan.Asset,
			Status:              string(types.TxStatusConfirming),
			RequiredConfs:       int(adapter.RequiredConfirmations()),
			Origin:              origin,
			ParentTransactionID: opts.ParentTransactionID,
		}
		if token != nil {
			tx.TokenContract = token.Contract
		}

		if err := s.txRepo.Create(tx); err != nil {
			return "", fmt.Errorf("persist %s tx: %w", txType, err)
		}

		if !isGasSeed {
			finalSweepHash = hash
			if s.webhookSvc != nil {
				s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventSweepBroadcast, tx)
			}
		}
	}

	return finalSweepHash, nil
}

// broadcastWithdrawal executes the single "final" transfer of a plan: sending
// `plan.Amount` of `plan.Asset` from `source` to `toAddress`. It is used for
// direct_from_base, direct_from_child, and the closing leg of multi_sweep
// (where source == wallet base deposit address).
func (s *service) broadcastWithdrawal(
	ctx context.Context,
	adapter types.Chain,
	curve mpcpkg.Curve,
	shareA, shareB []byte,
	wallet *models.Wallet,
	plan *Plan,
	source models.Address,
	toAddress, externalUserID string,
	txID uuid.UUID,
) (*models.Transaction, error) {
	var token *types.Token
	if plan.Asset != adapter.NativeAsset() {
		t, err := s.registry.FindToken(plan.Chain, plan.Asset)
		if err != nil {
			return nil, fmt.Errorf("find token %q: %w", plan.Asset, err)
		}
		token = t
	}

	unsigned, err := adapter.BuildTransfer(ctx, types.TransferRequest{
		From:   source.Address,
		To:     toAddress,
		Amount: plan.Amount,
		Asset:  plan.Asset,
		Token:  token,
	})
	if err != nil {
		return nil, fmt.Errorf("build transfer: %w", err)
	}

	sig, err := s.mpc.Sign(ctx, curve, shareA, shareB, mpcpkg.SignInputs{
		TxHashes: [][]byte{unsigned.RawBytes},
	})
	if err != nil {
		return nil, fmt.Errorf("mpc sign: %w", err)
	}

	signed := &types.SignedTx{ChainID: unsigned.ChainID, RawBytes: sig}
	hash, err := adapter.BroadcastTransaction(ctx, signed)
	if err != nil {
		return nil, fmt.Errorf("broadcast: %w", err)
	}

	sourceID := source.ID
	tx := &models.Transaction{
		ID:             txID,
		WalletID:       wallet.ID,
		AddressID:      &sourceID,
		ExternalUserID: externalUserID,
		Chain:          plan.Chain,
		TxType:         models.TxTypeWithdrawal,
		TxHash:         hash,
		FromAddress:    source.Address,
		ToAddress:      toAddress,
		Amount:         plan.Amount.String(),
		Asset:          plan.Asset,
		Status:         string(types.TxStatusConfirming),
		RequiredConfs:  int(adapter.RequiredConfirmations()),
		Origin:         models.TxOriginUserRequest,
	}
	if token != nil {
		tx.TokenContract = token.Contract
	}

	if err := s.txRepo.Create(tx); err != nil {
		return nil, fmt.Errorf("persist withdrawal: %w", err)
	}
	if s.webhookSvc != nil {
		s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventWithdrawalBroadcast, tx)
	}
	return tx, nil
}

// fetchShareB resolves the service's MPC share for `wallet`. Production paths
// go through AWS Secrets Manager; tests override the behaviour by assigning
// `service.fetchShareBFn` directly.
func (s *service) fetchShareB(ctx context.Context, wallet *models.Wallet) ([]byte, error) {
	if s.fetchShareBFn != nil {
		return s.fetchShareBFn(ctx, wallet)
	}
	if s.secrets == nil {
		return nil, fmt.Errorf("sweep: secrets manager not configured")
	}
	if wallet.MPCSecretARN == "" {
		return nil, fmt.Errorf("sweep: wallet has no MPC secret ARN")
	}
	arn := wallet.MPCSecretARN
	out, err := s.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &arn,
	})
	if err != nil {
		return nil, fmt.Errorf("sweep: fetch share_b: %w", err)
	}
	return out.SecretBinary, nil
}

// zeroBytes clears a byte slice in place to reduce the lifetime of sensitive
// key material in memory. Safe on nil.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
