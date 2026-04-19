package sweep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sort"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

// ConsolidateAll sweeps every eligible child balance for `asset` into the
// wallet's base deposit address. It is the manual counterpart to the
// planner/executor pipeline: there is no target amount and no final
// withdrawal — the sweep rows themselves are the terminal state.
//
// v1 is EVM-only. Non-EVM chains return ErrUnsupportedChain. The call is
// serialised per-wallet via acquireWalletOpsLock and metered per-account via
// incrDailyQuota, matching the withdrawal path so manual consolidations
// share the same operational envelope.
//
// The function is best-effort idempotent: if a sweep leg fails mid-flight,
// the successful legs are kept and the failure is reported on
// Result.FailedStep so the caller can retry from the remaining children
// without redoing work.
func (s *service) ConsolidateAll(
	ctx context.Context,
	walletID uuid.UUID,
	asset string,
	passphrase string,
) (*Result, error) {
	if len(passphrase) < 12 {
		return nil, fmt.Errorf("passphrase must be at least 12 characters")
	}

	wallet, err := s.walletRepo.FindByID(walletID)
	if err != nil {
		return nil, fmt.Errorf("sweep: find wallet: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("sweep: wallet %s not found", walletID)
	}
	if wallet.DepositAddress == nil {
		return nil, fmt.Errorf("sweep: wallet %s has no base deposit address", walletID)
	}

	chainEntity, err := s.chainRepo.FindByID(wallet.Chain)
	if err != nil {
		return nil, fmt.Errorf("sweep: find chain %q: %w", wallet.Chain, err)
	}
	if chainEntity == nil {
		return nil, fmt.Errorf("sweep: chain %q not found", wallet.Chain)
	}
	if chainEntity.AdapterType != models.AdapterTypeEVM {
		return nil, ErrUnsupportedChain
	}

	release, err := s.acquireWalletOpsLock(ctx, walletID)
	if err != nil {
		return nil, err
	}
	defer release()

	var accountID uuid.UUID
	if wallet.AccountID != nil {
		accountID = *wallet.AccountID
	}
	limits, err := s.LoadLimits(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if err := s.incrDailyQuota(ctx, accountID, limits); err != nil {
		return nil, err
	}

	adapter, err := s.registry.Chain(wallet.Chain)
	if err != nil {
		return nil, fmt.Errorf("sweep: adapter not registered for %q: %w", wallet.Chain, err)
	}

	children, err := s.addressRepo.FindByWalletID(walletID)
	if err != nil {
		return nil, fmt.Errorf("sweep: list children: %w", err)
	}

	dust := adapter.DustThreshold(asset)
	type childBal struct {
		addr    models.Address
		balance *big.Int
	}
	eligible := make([]childBal, 0, len(children))
	for _, c := range children {
		if c.ID == wallet.DepositAddress.ID {
			continue
		}
		bal, berr := fetchBalance(ctx, s.registry, wallet.Chain, adapter, c, asset)
		if berr != nil || bal == nil || bal.Sign() == 0 {
			continue
		}
		// Dust is skipped silently here. The planner surfaces it in
		// plan.DustIgnored for transparency to callers who can choose to
		// retry with a lower target; for manual consolidation there is no
		// target to adjust, so dust simply drops.
		if dust != nil && dust.Sign() > 0 && bal.Cmp(dust) < 0 {
			continue
		}
		eligible = append(eligible, childBal{addr: c, balance: new(big.Int).Set(bal)})
	}

	if len(eligible) == 0 {
		return &Result{}, nil
	}

	if err := checkAddressesPerRequest(chainEntity.AdapterType, len(eligible), limits); err != nil {
		return nil, err
	}

	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].balance.Cmp(eligible[j].balance) > 0
	})

	totalAmount := new(big.Int)
	sweeps := make([]PlannedSweep, 0, len(eligible))
	for _, cb := range eligible {
		sweeps = append(sweeps, PlannedSweep{
			From:     cb.addr,
			Amount:   new(big.Int).Set(cb.balance),
			NeedsGas: chainNeedsGasSeed(wallet.Chain),
		})
		totalAmount.Add(totalAmount, cb.balance)
	}
	plan := &Plan{
		WalletID:      walletID,
		Chain:         wallet.Chain,
		Asset:         asset,
		Amount:        totalAmount,
		Strategy:      StrategyMultiSweep,
		Sweeps:        sweeps,
		ReachesTarget: true,
	}

	shareA, err := s.decryptShareA(wallet, passphrase)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(shareA)

	shareB, err := s.fetchShareB(ctx, wallet)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(shareB)

	curve := mpcpkg.Curve(wallet.MPCCurve)
	result := &Result{}

	for i, leg := range plan.Sweeps {
		sweepTxID := uuid.New()
		hash, err := s.broadcastConsolidateLeg(
			ctx, adapter, curve, shareA, shareB, wallet, plan, leg, sweepTxID,
		)
		if err != nil {
			slog.Error(
				"consolidate leg failed",
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
	return result, nil
}

// broadcastConsolidateLeg mirrors broadcastSweepLeg but tags every resulting
// row with Origin=manual_consolidation and leaves parent_transaction_id
// NULL — manual consolidation has no parent withdrawal. On EVM ERC-20 this
// still produces an optional gas_seed tx (base → child native) followed by
// the sweep (child → base token). The gas_seed row keeps its own
// origin/tx_type tagging so it is not confused with the sweep it enables.
func (s *service) broadcastConsolidateLeg(
	ctx context.Context,
	adapter types.Chain,
	curve mpcpkg.Curve,
	shareA, shareB []byte,
	wallet *models.Wallet,
	plan *Plan,
	leg PlannedSweep,
	sweepTxID uuid.UUID,
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
		return "", fmt.Errorf("adapter returned no txs for consolidate leg")
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

		origin := models.TxOriginManualConsolidation
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
			ParentTransactionID: nil,
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

// decryptShareA reverses the AES-GCM envelope stored on the wallet row so the
// service can co-sign sweep transactions without ever persisting the
// plaintext share. The passphrase is expected to be caller-supplied
// (dashboard prompt / API header). The consolidate path maps
// mpcpkg.ErrInvalidPassphrase to a plain "invalid passphrase" error;
// rate-limiting of retries is not applied here (manual flow).
func (s *service) decryptShareA(wallet *models.Wallet, passphrase string) ([]byte, error) {
	shareA, err := wallet.DecryptShareA(passphrase)
	if err != nil {
		if errors.Is(err, mpcpkg.ErrInvalidPassphrase) {
			return nil, fmt.Errorf("invalid passphrase")
		}
		return nil, err
	}
	return shareA, nil
}
