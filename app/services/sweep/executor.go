package sweep

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

// walletKeys is the key material signUnsigned may need for one wallet. The callers
// that build it own shareA and shareB and zero them.
type walletKeys struct {
	shareA     []byte
	shareB     []byte
	passphrase string
}

// ExecutePlan runs a Plan against the live chain, co-signing with the provided
// ShareA (already decrypted by the caller from the wallet passphrase). It
// persists transaction rows with parent/origin tagging, emits webhooks per
// sweep leg, and reports partial failures so callers can retry idempotently
// from where the plan stopped.
func (s *service) ExecutePlan(
	ctx context.Context,
	plan *Plan,
	creds SigningCredentials,
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

	wallet, err := s.walletRepo.FindByID(ctx, plan.WalletID)
	if err != nil {
		return nil, fmt.Errorf("sweep: find wallet: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("sweep: wallet %s not found", plan.WalletID)
	}
	if wallet.DepositAddress == nil {
		return nil, fmt.Errorf("sweep: wallet %s has no deposit address", plan.WalletID)
	}

	if wallet.Chain != plan.Chain {
		return nil, fmt.Errorf("sweep: wallet %s is on %s, plan is on %s", wallet.ID, wallet.Chain, plan.Chain)
	}
	adapter, err := s.registry.ChainForWallet(wallet)
	if err != nil {
		return nil, fmt.Errorf("sweep: adapter for %q: %w", plan.Chain, err)
	}

	shareB, err := s.fetchShareB(ctx, wallet)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(shareB)

	curve := mpcpkg.Curve(wallet.MPCCurve)
	keys := walletKeys{shareA: creds.ShareA, shareB: shareB, passphrase: creds.Passphrase}
	result := &Result{WithdrawalTxID: withdrawalTxID, EstimatedGas: copyBigInt(plan.EstimatedGas)}

	switch plan.Strategy {
	case StrategyDirectFromBase, StrategyDirectFromChild:
		if plan.SourceAddress == nil {
			return nil, fmt.Errorf("sweep: plan source address missing for strategy %s", plan.Strategy)
		}
		finalTx, err := s.broadcastWithdrawal(
			ctx, adapter, curve, keys, wallet, plan,
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
				ctx, adapter, curve, keys, wallet, plan,
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
			ctx, adapter, curve, keys, wallet, plan,
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
//
// A withdrawal-driven leg (ParentTransactionID set) and a manual consolidation
// (no parent) both commit the gas seed, the sweep row, and the sweep.broadcast
// webhook event in one transaction after the broadcasts. A failed later insert
// rolls the earlier rows back. SQS runs only after that commit. The final
// withdrawal row and gas-status updates stay outside this commit.
func (s *service) broadcastLeg(
	ctx context.Context,
	adapter types.Chain,
	curve mpcpkg.Curve,
	keys walletKeys,
	wallet *models.Wallet,
	plan *Plan,
	leg PlannedSweep,
	sweepTxID uuid.UUID,
	opts legBroadcastOpts,
) (string, error) {
	return s.broadcastLinkedLeg(ctx, adapter, curve, keys, wallet, plan, leg, sweepTxID, opts)
}

// sweepBroadcastStager inserts sweep.broadcast webhook rows on the caller's
// transaction. The returned send runs only after that transaction commits.
type sweepBroadcastStager interface {
	StageSweepBroadcast(ctx context.Context, tx *models.Transaction) (func(context.Context), error)
}

type linkedLegRow struct {
	tx      *models.Transaction
	gasSeed bool
}

// broadcastLinkedLeg is the withdrawal-driven sweep leg. Chain broadcasts stay
// outside the database transaction. Rows whose broadcasts already succeeded are
// committed together, so a failed sweep insert or webhook insert rolls back the
// gas seed from that same commit. A later broadcast failure still keeps the
// earlier broadcast's row: that row is committed on its own before the error
// returns, because the sweep row was never written.
func (s *service) broadcastLinkedLeg(
	ctx context.Context,
	adapter types.Chain,
	curve mpcpkg.Curve,
	keys walletKeys,
	wallet *models.Wallet,
	plan *Plan,
	leg PlannedSweep,
	sweepTxID uuid.UUID,
	opts legBroadcastOpts,
) (string, error) {
	unsigneds, token, err := s.buildSweepLeg(ctx, adapter, wallet, plan, leg)
	if err != nil {
		return "", err
	}

	var finalSweepHash string
	pending := make([]linkedLegRow, 0, len(unsigneds))
	for idx := range unsigneds {
		unsigned := unsigneds[idx]
		signer, isGasSeed := sweepLegSigner(wallet, leg, len(unsigneds), idx)
		signed, signErr := s.signUnsigned(ctx, adapter, curve, keys, wallet, signer, &unsigned)
		if signErr != nil {
			if commitErr := s.commitLinkedLeg(ctx, pending); commitErr != nil {
				return "", commitErr
			}
			return "", fmt.Errorf("sign transaction: %w", signErr)
		}
		hash, bcErr := adapter.BroadcastTransaction(ctx, signed)
		if bcErr != nil {
			if commitErr := s.commitLinkedLeg(ctx, pending); commitErr != nil {
				return "", commitErr
			}
			return "", fmt.Errorf("broadcast: %w", bcErr)
		}
		pending = append(pending, linkedLegRow{
			tx:      sweepLegTransaction(wallet, plan, leg, sweepTxID, opts, &unsigned, hash, token, isGasSeed, adapter),
			gasSeed: isGasSeed,
		})
		if !isGasSeed {
			finalSweepHash = hash
		}
	}
	if err := s.commitLinkedLeg(ctx, pending); err != nil {
		return "", err
	}
	return finalSweepHash, nil
}

func (s *service) commitLinkedLeg(ctx context.Context, rows []linkedLegRow) error {
	if len(rows) == 0 {
		return nil
	}
	if s.txRepo == nil {
		return fmt.Errorf("persist sweep tx: transaction writer is required")
	}
	var sends []func(context.Context)
	err := s.txRepo.Within(ctx, func(txCtx context.Context) error {
		for _, row := range rows {
			if err := s.txRepo.Create(txCtx, row.tx); err != nil {
				return fmt.Errorf("persist %s tx: %w", row.tx.TxType, err)
			}
		}
		if s.webhookSvc == nil {
			return nil
		}
		var stager sweepBroadcastStager
		for _, row := range rows {
			if row.gasSeed {
				continue
			}
			if stager == nil {
				var ok bool
				stager, ok = s.webhookSvc.(sweepBroadcastStager)
				if !ok {
					return fmt.Errorf("persist sweep webhook: event writer cannot join the transaction")
				}
			}
			send, stageErr := stager.StageSweepBroadcast(txCtx, row.tx)
			if stageErr != nil {
				return fmt.Errorf("persist sweep webhook: %w", stageErr)
			}
			if send != nil {
				sends = append(sends, send)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, send := range sends {
		send(ctx)
	}
	return nil
}

func sweepLegTransaction(
	wallet *models.Wallet,
	plan *Plan,
	leg PlannedSweep,
	sweepTxID uuid.UUID,
	opts legBroadcastOpts,
	unsigned *types.UnsignedTx,
	hash string,
	token *types.Token,
	isGasSeed bool,
	adapter types.Chain,
) *models.Transaction {
	origin := opts.Origin
	txType := models.TxTypeSweep
	txID := sweepTxID
	fromAddr := leg.From.Address
	toAddr := wallet.DepositAddress.Address
	childID := leg.From.ID
	addressID := &childID
	asset := plan.Asset
	amount := builtAmount(unsigned, leg.Amount)
	rowToken := token
	if isGasSeed {
		origin = models.TxOriginGasSeed
		txType = models.TxTypeGasSeed
		txID = uuid.New()
		fromAddr = wallet.DepositAddress.Address
		toAddr = leg.From.Address
		baseID := wallet.DepositAddress.ID
		addressID = &baseID
		asset = adapter.NativeAsset()
		amount = builtAmount(unsigned, nil)
		rowToken = nil
	}
	tx := &models.Transaction{
		ID:                  txID,
		WalletID:            wallet.ID,
		AddressID:           addressID,
		ExternalUserID:      leg.From.ExternalUserID,
		Chain:               plan.Chain,
		TxType:              txType,
		TxHash:              hash,
		FromAddress:         fromAddr,
		ToAddress:           toAddr,
		Amount:              amount,
		Asset:               asset,
		Status:              string(types.TxStatusConfirming),
		RequiredConfs:       int(adapter.RequiredConfirmations()),
		Direction:           models.TxDirectionSelf,
		Source:              models.TxSourceWithdrawalFlow,
		Origin:              origin,
		RawPayload:          "{}",
		ParentTransactionID: opts.ParentTransactionID,
	}
	if rowToken != nil {
		tx.TokenContract = rowToken.Contract
	}
	return tx
}

// withdrawalBroadcastStager inserts withdrawal.broadcasting webhook rows on the
// caller's transaction. The returned send runs only after that transaction commits.
type withdrawalBroadcastStager interface {
	StageWithdrawalBroadcasting(ctx context.Context, tx *models.Transaction) (func(context.Context), error)
}

// broadcastWithdrawal executes the single "final" transfer of a plan: sending
// `plan.Amount` of `plan.Asset` from `source` to `toAddress`. It is used for
// direct_from_base, direct_from_child, and the closing leg of multi_sweep
// (where source == wallet base deposit address).
//
// The chain broadcast stays outside the database transaction. The withdrawal
// row and its withdrawal.broadcasting webhook commit together. A failed webhook
// insert rolls the withdrawal row back. SQS runs only after that commit.
func (s *service) broadcastWithdrawal(
	ctx context.Context,
	adapter types.Chain,
	curve mpcpkg.Curve,
	keys walletKeys,
	wallet *models.Wallet,
	plan *Plan,
	source models.Address,
	toAddress, externalUserID string,
	txID uuid.UUID,
) (*models.Transaction, error) {
	signed, token, err := s.signWithdrawalTransfer(ctx, adapter, curve, keys, wallet, plan, source, toAddress)
	if err != nil {
		return nil, err
	}
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
		Direction:      models.TxDirectionOutbound,
		Source:         models.TxSourceWithdrawalFlow,
		Origin:         models.TxOriginUserRequest,
		RawPayload:     "{}",
	}
	if token != nil {
		tx.TokenContract = token.Contract
	}

	var send func(context.Context)
	if err := s.txRepo.Within(ctx, func(txCtx context.Context) error {
		if err := s.txRepo.Create(txCtx, tx); err != nil {
			return fmt.Errorf("persist withdrawal: %w", err)
		}
		if s.webhookSvc == nil {
			return nil
		}
		stager, ok := s.webhookSvc.(withdrawalBroadcastStager)
		if !ok {
			return fmt.Errorf("persist withdrawal webhook: event writer cannot join the transaction")
		}
		staged, stageErr := stager.StageWithdrawalBroadcasting(txCtx, tx)
		if stageErr != nil {
			return fmt.Errorf("persist withdrawal webhook: %w", stageErr)
		}
		send = staged
		return nil
	}); err != nil {
		return nil, err
	}
	if send != nil {
		send(ctx)
	}
	return tx, nil
}

// signWithdrawalTransfer builds and signs the transfer of plan.Amount from source to
// toAddress; the signature is verified against source before it is returned.
func (s *service) signWithdrawalTransfer(
	ctx context.Context,
	adapter types.Chain,
	curve mpcpkg.Curve,
	keys walletKeys,
	wallet *models.Wallet,
	plan *Plan,
	source models.Address,
	toAddress string,
) (*types.SignedTx, *types.Token, error) {
	token, err := s.planAssetToken(adapter, plan)
	if err != nil {
		return nil, nil, err
	}
	unsigned, err := adapter.BuildTransfer(ctx, types.TransferRequest{
		From:   source.Address,
		To:     toAddress,
		Amount: plan.Amount,
		Asset:  plan.Asset,
		Token:  token,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build transfer: %w", err)
	}
	signed, err := s.signUnsigned(ctx, adapter, curve, keys, wallet, source, unsigned)
	if err != nil {
		return nil, nil, fmt.Errorf("sign transaction: %w", err)
	}
	return signed, token, nil
}

// planAssetToken is the token a plan moves, or nil for the chain's native asset.
func (s *service) planAssetToken(adapter types.Chain, plan *Plan) (*types.Token, error) {
	if types.SameAssetSymbol(plan.Asset, adapter.NativeAsset()) {
		return nil, nil
	}
	token, err := s.registry.FindToken(plan.Chain, plan.Asset)
	if err != nil {
		return nil, fmt.Errorf("find token %q: %w", plan.Asset, err)
	}
	return token, nil
}

// buildSweepLeg returns the unsigned transactions of one sweep leg, in broadcast
// order: an optional gas seed from the base address, then the sweep itself.
func (s *service) buildSweepLeg(
	ctx context.Context,
	adapter types.Chain,
	wallet *models.Wallet,
	plan *Plan,
	leg PlannedSweep,
) ([]types.UnsignedTx, *types.Token, error) {
	nativeBal, err := adapter.GetBalance(ctx, leg.From.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("native balance for %s: %w", leg.From.Address, err)
	}
	token, err := s.planAssetToken(adapter, plan)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, fmt.Errorf("build sweep: %w", err)
	}
	if len(unsigneds) == 0 {
		return nil, nil, fmt.Errorf("adapter returned no txs for sweep leg")
	}
	return unsigneds, token, nil
}

// unknownBuiltAmount is recorded for a gas seed whose adapter does not report the
// value it encoded; the planned leg amount is denominated in the swept token.
const unknownBuiltAmount = "0"

// builtAmount is the amount the unsigned tx moves, falling back to planned when the
// adapter does not report it. Native EVM sweeps re-size at build time, so the plan
// can differ from what is broadcast.
func builtAmount(unsigned *types.UnsignedTx, planned *big.Int) string {
	if unsigned != nil && unsigned.TransferAmount != nil {
		return unsigned.TransferAmount.String()
	}
	if planned == nil {
		return unknownBuiltAmount
	}
	return planned.String()
}

// sweepLegSigner is the address whose key signs transaction idx of a leg: the base
// address for the gas seed, the child for the sweep.
func sweepLegSigner(wallet *models.Wallet, leg PlannedSweep, legTxCount, idx int) (models.Address, bool) {
	isGasSeed := legTxCount > 1 && idx == 0
	if isGasSeed {
		return *wallet.DepositAddress, true
	}
	return leg.From, false
}

func localSignChain(chainID string) bool {
	switch chainID {
	case models.ChainSOL, models.ChainTSOL, models.ChainBTC, models.ChainTBTC:
		return true
	default:
		return false
	}
}

// signUnsigned signs a transaction whose only signer is `signer`, with the key that
// owns signer's address: the wallet key for the base address, its derived key for a
// child.
func (s *service) signUnsigned(ctx context.Context, adapter types.Chain, curve mpcpkg.Curve, keys walletKeys, wallet *models.Wallet, signer models.Address, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	if unsigned == nil {
		return nil, fmt.Errorf("unsigned transaction is required")
	}
	switch curve {
	case mpcpkg.CurveEd25519:
		if !localSignChain(unsigned.ChainID) {
			return nil, fmt.Errorf("chain %s cannot sign with an ed25519 wallet", unsigned.ChainID)
		}
		return s.signEd25519(ctx, adapter, keys, wallet, signer, unsigned)
	case mpcpkg.CurveSecp256k1:
		key, err := resolveSecp256k1Signer(adapter, wallet, signer)
		if err != nil {
			return nil, err
		}
		var signed *types.SignedTx
		if localSignChain(unsigned.ChainID) {
			signed, err = s.signSecp256k1Local(ctx, adapter, keys, key, wallet.MPCPublicKey, unsigned)
		} else {
			signed, err = s.signSecp256k1MPC(ctx, adapter, keys, key, unsigned)
		}
		if err != nil {
			return nil, err
		}
		if err := verifySignedBy(adapter, unsigned, signed, signer.Address); err != nil {
			return nil, err
		}
		return signed, nil
	default:
		return nil, fmt.Errorf("unsupported curve %s", curve)
	}
}

// finalizeMPCTransaction attaches the threshold signature; finalizing adapters
// (EVM) recover the signer against publicKey and fail when it does not match.
func finalizeMPCTransaction(
	adapter types.Chain,
	unsigned *types.UnsignedTx,
	signature []byte,
	publicKey []byte,
) (*types.SignedTx, error) {
	if adapter == nil {
		return nil, fmt.Errorf("chain adapter is required")
	}
	if unsigned == nil {
		return nil, fmt.Errorf("unsigned transaction is required")
	}
	if len(signature) == 0 {
		return nil, fmt.Errorf("MPC signature is required")
	}

	finalizer, requiresFinalization := adapter.(types.MPCSignatureFinalizer)
	if !requiresFinalization {
		return &types.SignedTx{ChainID: unsigned.ChainID, RawBytes: signature}, nil
	}
	if len(publicKey) == 0 {
		return nil, fmt.Errorf("signing public key is required")
	}
	return finalizer.FinalizeMPCSignature(unsigned, signature, publicKey)
}

// fetchShareB resolves the service's MPC share for `wallet`. Production paths
// go through the injected secret reader; tests override the behaviour by assigning
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
	binary, err := s.secrets.Binary(ctx, wallet.MPCSecretARN)
	if err != nil {
		return nil, fmt.Errorf("sweep: fetch share_b: %w", err)
	}
	return binary, nil
}

// zeroBytes clears a byte slice in place to reduce the lifetime of sensitive
// key material in memory. Safe on nil.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// copyBigInt defensively clones a *big.Int so Plan → Result copies do not
// share backing storage. Safe on nil (returns nil).
func copyBigInt(v *big.Int) *big.Int {
	if v == nil {
		return nil
	}
	return new(big.Int).Set(v)
}
