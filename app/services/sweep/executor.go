package sweep

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/mpcshare"
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
	defer mpcshare.DiscardPassphrase(&creds.Passphrase)
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
	defer mpcshare.DiscardPassphrase(&keys.passphrase)
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
			completed, err := s.broadcastLeg(
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
					"provider_cause", chain.CauseText(err),
				)
				result.FailedStep = &FailedStep{
					Index:      i,
					LastError:  chain.ClientText(err),
					RetryReady: true,
				}
				return result, nil
			}
			result.Sweeps = append(result.Sweeps, completed)
		}
		for _, sweep := range result.Sweeps {
			if err := awaitInclusion(ctx, adapter, sweep.TxHash); err != nil {
				return result, fmt.Errorf("sweep: sweep from %s: %w", sweep.From.Address, err)
			}
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
// Returns the completed sweep: its on-chain hash (not the gas_seed's) and the
// amount it moved.
//
// Gas seeds are broadcast and awaited before the sweep (TRON pays the child
// from the seed). A shortfall is topped up, at most twice, before the sweep
// is signed. The sweep row and its sweep.broadcast webhook commit in one
// transaction after that broadcast. A failed webhook insert rolls the sweep
// row back. SQS runs only after that commit.
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
) (CompletedSweep, error) {
	unsigneds, token, err := s.buildSweepLeg(ctx, adapter, wallet, plan, leg)
	if err != nil {
		return CompletedSweep{}, err
	}

	completed := CompletedSweep{From: leg.From, InternalTxID: sweepTxID}
	var sweepRow *models.Transaction
	for idx := range unsigneds {
		unsigned := unsigneds[idx]
		signer, isGasSeed := sweepLegSigner(wallet, leg, len(unsigneds), idx)
		if isGasSeed {
			if err := s.broadcastGasSeed(ctx, adapter, curve, keys, wallet, plan, leg, &unsigned, opts); err != nil {
				return CompletedSweep{}, err
			}
			continue
		}
		if err := s.fundSweep(ctx, adapter, curve, keys, wallet, plan, leg, &unsigned, opts); err != nil {
			return CompletedSweep{}, err
		}

		signed, signErr := s.signUnsigned(ctx, adapter, curve, keys, wallet, signer, &unsigned)
		if signErr != nil {
			return CompletedSweep{}, fmt.Errorf("sign transaction: %w", signErr)
		}
		hash, bcErr := adapter.BroadcastTransaction(ctx, signed)
		if bcErr != nil {
			return CompletedSweep{}, fmt.Errorf("broadcast: %w", bcErr)
		}
		sweepRow = sweepLegTransaction(wallet, plan, leg, sweepTxID, opts, &unsigned, hash, token, false, adapter)
		completed.TxHash = hash
		completed.Amount = sweptAmount(&unsigned, leg.Amount)
	}
	if sweepRow != nil {
		if err := s.commitLinkedLeg(ctx, []linkedLegRow{{tx: sweepRow}}); err != nil {
			return CompletedSweep{}, err
		}
	}
	return completed, nil
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

// broadcastGasSeed signs a base to child native transfer with the base key,
// broadcasts it, records it as a gas_seed row and waits until it is in a block
// where the chain requires it. A seed whose broadcast outcome is unknown (with a
// signed hash) is recorded and awaited the same way: it may land, and failing the
// leg would make the next run seed the child again.
func (s *service) broadcastGasSeed(
	ctx context.Context,
	adapter types.Chain,
	curve mpcpkg.Curve,
	keys walletKeys,
	wallet *models.Wallet,
	plan *Plan,
	leg PlannedSweep,
	unsigned *types.UnsignedTx,
	opts legBroadcastOpts,
) error {
	signed, err := s.signUnsigned(ctx, adapter, curve, keys, wallet, *wallet.DepositAddress, unsigned)
	if err != nil {
		return fmt.Errorf("sign transaction: %w", err)
	}
	head := s.broadcastHead(ctx, adapter, plan.Chain)
	hash, err := adapter.BroadcastTransaction(ctx, signed)
	unknown := err != nil && strings.TrimSpace(signed.TxHash) != "" && errors.Is(err, chain.ErrUnknownOutcome)
	if err != nil && !unknown {
		return fmt.Errorf("broadcast: %w", err)
	}
	if unknown {
		hash = signed.TxHash
		slog.Warn("gas_seed broadcast outcome unknown; recorded and awaited by its signed hash",
			"chain", plan.Chain, "tx_hash", hash, "child", leg.From.Address, "error", chain.ClientText(err))
	}
	tx := sweepLegTransaction(wallet, plan, leg, uuid.New(), opts, unsigned, hash, nil, true, adapter)
	if unknown {
		if tx.RawPayload, err = broadcastRawPayload(adapter, signed, head, chain.BroadcastOutcomeUnknown); err != nil {
			return fmt.Errorf("record gas_seed payload: %w", err)
		}
	}
	if err := s.txRepo.Create(ctx, tx); err != nil {
		return fmt.Errorf("persist %s tx: %w", models.TxTypeGasSeed, err)
	}
	if err := awaitInclusion(ctx, adapter, hash); err != nil {
		return fmt.Errorf("gas_seed: %w", err)
	}
	return nil
}

// sweepFundingChecker is implemented by chains that size a gas_seed by the sweep's
// estimated cost rather than its ceiling, and can re-price that cost right before
// the sweep (TRON: the contract deployer's share of the energy can run out).
type sweepFundingChecker interface {
	SweepFundingShortfall(ctx context.Context, sweep *types.UnsignedTx) (*big.Int, error)
}

// sweepFundingTopUps bounds the extra gas_seeds sent for one sweep when its sender
// is still short after the first.
const sweepFundingTopUps = 2

// fundSweep tops the sweep's sender up with what it still lacks (each top-up a
// gas_seed awaited like the first) and refuses the sweep when the sender stays
// short or its shortfall cannot be read.
func (s *service) fundSweep(
	ctx context.Context,
	adapter types.Chain,
	curve mpcpkg.Curve,
	keys walletKeys,
	wallet *models.Wallet,
	plan *Plan,
	leg PlannedSweep,
	sweep *types.UnsignedTx,
	opts legBroadcastOpts,
) error {
	checker, ok := adapter.(sweepFundingChecker)
	if !ok {
		return nil
	}
	for topUps := 0; ; topUps++ {
		shortfall, err := checker.SweepFundingShortfall(ctx, sweep)
		if err != nil {
			return fmt.Errorf("sweep funding of %s: %w", leg.From.Address, err)
		}
		if shortfall == nil || shortfall.Sign() <= 0 {
			return nil
		}
		if topUps == sweepFundingTopUps {
			return fmt.Errorf("sweep sender %s still lacks %s %s after %d gas_seed top-ups", leg.From.Address, shortfall, adapter.NativeAsset(), topUps)
		}
		slog.Info("topping up sweep sender", "chain", plan.Chain, "address", leg.From.Address, "shortfall", shortfall.String())
		seed, err := adapter.BuildTransfer(ctx, types.TransferRequest{
			From: wallet.DepositAddress.Address, To: leg.From.Address, Amount: shortfall, Asset: adapter.NativeAsset(),
		})
		if err != nil {
			return fmt.Errorf("build gas_seed top-up: %w", err)
		}
		if err := s.broadcastGasSeed(ctx, adapter, curve, keys, wallet, plan, leg, seed, opts); err != nil {
			return err
		}
	}
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
		LogIndex:            models.TransactionLogIndexNone,
		FromAddress:         fromAddr,
		ToAddress:           toAddr,
		Amount:              amount,
		Asset:               asset,
		Status:              string(types.TxStatusConfirming),
		RequiredConfs:       int(adapter.RequiredConfirmations()),
		Finality:            chain.FinalityPolicyOf(adapter),
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
// The row is recorded under the hash of the signed bytes before they leave (a row
// that cannot be recorded stops the withdrawal before anything is sent). From the
// send on, the withdrawal is never failed for a transfer that may land: an answer
// that does not say whether the network took it (chain.ErrUnknownOutcome) keeps
// the row, marked broadcast_outcome unknown, and a recording or webhook insert that
// fails after the send is retried, then logged, with the row left for the tracker
// to settle by its hash. A definite refusal marks the row failed. The
// withdrawal.broadcasting webhook is staged in a database transaction after the
// send; SQS runs only after it commits. A signed transaction without a hash (no
// adapter in production) is recorded after the send, as before.
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
	head := s.broadcastHead(ctx, adapter, plan.Chain)
	rawPayload, err := s.pendingPayload(adapter, signed, head)
	if err != nil {
		return nil, err
	}
	tx := withdrawalRow(adapter, wallet, plan, source, toAddress, externalUserID, txID, signed.TxHash, rawPayload)
	if token != nil {
		tx.TokenContract = token.Contract
	}
	tracked := strings.TrimSpace(signed.TxHash) != ""
	if tracked {
		if err = s.recordBeforeSend(ctx, tx, plan.WithdrawalID); err != nil {
			return nil, fmt.Errorf("persist withdrawal: %w", err)
		}
	}

	hash, err := adapter.BroadcastTransaction(ctx, signed)
	unknown := err != nil && tracked && errors.Is(err, chain.ErrUnknownOutcome)
	if err != nil && !unknown {
		if tracked {
			s.markNotSent(ctx, adapter, signed, head, tx, err)
		}
		return nil, fmt.Errorf("broadcast: %w", err)
	}
	outcome := chain.BroadcastOutcomeAccepted
	if unknown {
		outcome = chain.BroadcastOutcomeUnknown
		hash = signed.TxHash
		slog.Warn("withdrawal broadcast outcome unknown; recorded and tracked by its signed hash",
			"tx_id", txID, "tx_hash", hash, "chain", plan.Chain, "error", chain.ClientText(err))
	}
	if tx.RawPayload, err = broadcastRawPayload(adapter, signed, head, outcome); err != nil {
		return nil, fmt.Errorf("record last valid block: %w", err)
	}
	if strings.TrimSpace(hash) == "" {
		hash = signed.TxHash
	}
	if !tracked {
		tx.TxHash = hash
		return tx, s.persistWithRetry(ctx, tx, "persist withdrawal", func(txCtx context.Context) error {
			return s.txRepo.Create(txCtx, tx)
		})
	}
	tx.TxHash = hash
	if err := s.persistWithRetry(ctx, tx, "record withdrawal broadcast", func(txCtx context.Context) error {
		return s.txRepo.RecordBroadcast(txCtx, tx.ID, tx.TxHash, tx.RawPayload)
	}); err != nil {
		// The bytes may be on their way: the row recorded before the send stays, and
		// the tracker settles it by its hash.
		slog.Error("withdrawal sent but its broadcast could not be recorded; the tracker settles it by its hash",
			"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", plan.Chain, "error", err)
	}
	return tx, nil
}

// broadcastHead is the chain head read just before a send, for an adapter that can
// prove which transaction spent a nonce (chain.NonceSpendReader, EVM): no earlier
// block can hold the transfer. 0 for any other adapter, or when the head cannot be
// read (logged: an unknown outcome of that transfer then cannot be proven).
func (s *service) broadcastHead(ctx context.Context, adapter types.Chain, chainID string) uint64 {
	if _, ok := adapter.(chain.NonceSpendReader); !ok {
		return 0
	}
	head, err := adapter.GetLatestBlock(ctx)
	if err != nil {
		slog.Warn("chain head before a withdrawal send could not be read", "chain", chainID, "error", err)
		return 0
	}
	return head
}

// recordBeforeSend creates the withdrawal's transaction row (under its idempotency
// key) and links the withdrawal to it, in one database transaction: either both are
// there before the bytes leave or nothing is, and nothing is sent.
func (s *service) recordBeforeSend(ctx context.Context, tx *models.Transaction, withdrawalID uuid.UUID) error {
	return s.txRepo.Within(ctx, func(txCtx context.Context) error {
		if err := s.txRepo.Create(txCtx, tx); err != nil {
			return err
		}
		if s.withdrawalLinks == nil || withdrawalID == uuid.Nil {
			return nil
		}
		if err := s.withdrawalLinks.LinkTransaction(txCtx, withdrawalID, tx.ID); err != nil {
			return fmt.Errorf("link withdrawal %s: %w", withdrawalID, err)
		}
		return nil
	})
}

// pendingPayload is the raw_payload of a withdrawal row recorded before its send:
// the pending outcome and, sealed, the signed bytes a same-key retry sends again if
// the send is never answered (none without a sealer). Bytes that cannot be sealed
// stop the withdrawal before anything is recorded or sent.
func (s *service) pendingPayload(adapter types.Chain, signed *types.SignedTx, head uint64) (string, error) {
	raw, err := broadcastRawPayload(adapter, signed, head, chain.BroadcastOutcomePending)
	if err != nil {
		return "", fmt.Errorf("record last valid block: %w", err)
	}
	if s.sealer == nil || signed == nil || len(signed.RawBytes) == 0 {
		return raw, nil
	}
	sealed, err := s.sealer.Seal(hex.EncodeToString(signed.RawBytes))
	if err != nil {
		return "", fmt.Errorf("seal the signed withdrawal: %w", err)
	}
	payload := map[string]any{}
	if decodeErr := json.Unmarshal([]byte(raw), &payload); decodeErr != nil {
		return "", fmt.Errorf("record the signed withdrawal: %w", decodeErr)
	}
	payload[chain.SignedTxPayloadKey] = sealed
	withBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("record the signed withdrawal: %w", err)
	}
	return string(withBytes), nil
}

// broadcastRefusedReason is the error_message of a withdrawal row recorded before
// its send and then refused by the network (nothing left).
const broadcastRefusedReason = "broadcast_refused"

// markNotSent marks a row recorded before a send that was refused (failed, its key
// released, the refused bytes dropped from raw_payload), retried like the recording
// of an answered send. A row that still cannot be marked keeps its pending outcome:
// a retry with its key then checks it (SettleUnanswered) and never replays it as
// broadcast. That is logged at error level.
func (s *service) markNotSent(ctx context.Context, adapter types.Chain, signed *types.SignedTx, head uint64, tx *models.Transaction, cause error) {
	refused, payloadErr := broadcastRawPayload(adapter, signed, head, chain.BroadcastOutcomeRefused)
	if payloadErr != nil {
		refused = `{"` + chain.BroadcastOutcomePayloadKey + `":"` + chain.BroadcastOutcomeRefused + `"}`
	}
	if err := s.retryWrite(ctx, tx, "mark refused withdrawal", func(txCtx context.Context) error {
		if err := s.txRepo.MarkFailed(txCtx, tx.ID, broadcastRefusedReason); err != nil {
			return err
		}
		return s.txRepo.RecordBroadcast(txCtx, tx.ID, tx.TxHash, refused)
	}); err != nil {
		slog.Error("withdrawal broadcast refused but its row could not be marked failed; a retry with its key checks it again",
			"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "broadcast_error", chain.ClientText(cause), "error", err)
		return
	}
	tx.RawPayload, tx.Status = refused, string(types.TxStatusFailed)
}

// persistRetryDelays are the waits before each new try of the recording that
// follows a send.
var persistRetryDelays = []time.Duration{200 * time.Millisecond, 600 * time.Millisecond}

// persistWithRetry runs write and stages the withdrawal.broadcasting webhook in one
// database transaction, retried after persistRetryDelays, and sends the webhook
// once it commits. A failed webhook insert rolls write back.
func (s *service) persistWithRetry(ctx context.Context, tx *models.Transaction, what string, write func(context.Context) error) error {
	var send func(context.Context)
	err := s.retryWrite(ctx, tx, what, func(txCtx context.Context) error {
		if writeErr := write(txCtx); writeErr != nil {
			return writeErr
		}
		staged, stageErr := s.stageWithdrawalBroadcasting(txCtx, tx)
		send = staged
		return stageErr
	})
	if err == nil && send != nil {
		send(ctx)
	}
	return err
}

// retryWrite runs write in one database transaction, retried after
// persistRetryDelays.
func (s *service) retryWrite(ctx context.Context, tx *models.Transaction, what string, write func(context.Context) error) error {
	var err error
	for attempt := 0; ; attempt++ {
		err = s.txRepo.Within(ctx, func(txCtx context.Context) error {
			if writeErr := write(txCtx); writeErr != nil {
				return fmt.Errorf("%s: %w", what, writeErr)
			}
			return nil
		})
		if err == nil {
			return nil
		}
		if attempt == len(persistRetryDelays) || ctx.Err() != nil {
			return err
		}
		slog.Warn("withdrawal recording failed, retrying", "tx_id", tx.ID, "attempt", attempt+1, "error", err)
		if sleepErr := s.sleepFor(ctx, persistRetryDelays[attempt]); sleepErr != nil {
			return err
		}
	}
}

// stageWithdrawalBroadcasting inserts the withdrawal.broadcasting webhook row on the
// caller's transaction; nil without a webhook writer.
func (s *service) stageWithdrawalBroadcasting(txCtx context.Context, tx *models.Transaction) (func(context.Context), error) {
	if s.webhookSvc == nil {
		return nil, nil
	}
	stager, ok := s.webhookSvc.(withdrawalBroadcastStager)
	if !ok {
		return nil, fmt.Errorf("persist withdrawal webhook: event writer cannot join the transaction")
	}
	staged, err := stager.StageWithdrawalBroadcasting(txCtx, tx)
	if err != nil {
		return nil, fmt.Errorf("persist withdrawal webhook: %w", err)
	}
	return staged, nil
}

func (s *service) sleepFor(ctx context.Context, d time.Duration) error {
	if s.sleep != nil {
		return s.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// withdrawalRow is the transactions row of a final withdrawal transfer.
func withdrawalRow(adapter types.Chain, wallet *models.Wallet, plan *Plan, source models.Address, toAddress, externalUserID string, txID uuid.UUID, txHash, rawPayload string) *models.Transaction {
	sourceID := source.ID
	return &models.Transaction{
		ID:             txID,
		WalletID:       wallet.ID,
		AddressID:      &sourceID,
		ExternalUserID: externalUserID,
		Chain:          plan.Chain,
		TxType:         models.TxTypeWithdrawal,
		TxHash:         txHash,
		LogIndex:       models.TransactionLogIndexNone,
		FromAddress:    source.Address,
		ToAddress:      toAddress,
		DestinationTag: copyTag(plan.DestinationTag),
		Amount:         plan.Amount.String(),
		Asset:          plan.Asset,
		Status:         string(types.TxStatusConfirming),
		RequiredConfs:  int(adapter.RequiredConfirmations()),
		Finality:       chain.FinalityPolicyOf(adapter),
		Direction:      models.TxDirectionOutbound,
		Source:         models.TxSourceWithdrawalFlow,
		Origin:         models.TxOriginUserRequest,
		RawPayload:     rawPayload,
		IdempotencyKey: idempotencyKeyOf(plan),
	}
}

// idempotencyKeyOf is the plan's idempotency key, nil without one.
func idempotencyKeyOf(plan *Plan) *string {
	key := strings.TrimSpace(plan.IdempotencyKey)
	if key == "" {
		return nil
	}
	return &key
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
	if plan.DestinationTag != nil && !supportsDestinationTag(adapter) {
		return nil, nil, ErrDestinationTagUnsupported
	}
	unsigned, err := adapter.BuildTransfer(ctx, types.TransferRequest{
		From:           source.Address,
		To:             toAddress,
		Amount:         plan.Amount,
		Asset:          plan.Asset,
		Token:          token,
		DestinationTag: copyTag(plan.DestinationTag),
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

// sweptAmount is builtAmount as a number: nil when neither the unsigned tx nor the
// plan knows it.
func sweptAmount(unsigned *types.UnsignedTx, planned *big.Int) *big.Int {
	if unsigned != nil && unsigned.TransferAmount != nil {
		return new(big.Int).Set(unsigned.TransferAmount)
	}
	if planned == nil {
		return nil
	}
	return new(big.Int).Set(planned)
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

// inclusionAwaiter is implemented by chains whose node refuses a transaction until
// the one it depends on is in a block: on TRON a child can spend only after its
// gas_seed created and funded its account, and a TRC-20 withdrawal from base is
// built only once base holds the swept tokens.
type inclusionAwaiter interface {
	AwaitTransactionIncluded(ctx context.Context, txHash string) error
}

func awaitInclusion(ctx context.Context, adapter types.Chain, txHash string) error {
	awaiter, ok := adapter.(inclusionAwaiter)
	if !ok {
		return nil
	}
	return awaiter.AwaitTransactionIncluded(ctx, txHash)
}

func localSignChain(chainID string) bool {
	switch {
	case chainID == models.ChainSOL, chainID == models.ChainTSOL:
		return true
	default:
		return models.IsBitcoinFamilyChainID(chainID)
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
