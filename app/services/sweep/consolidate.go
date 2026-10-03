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
// EVM, Solana, and Bitcoin wallets can be consolidated. Any other adapter returns ErrUnsupportedChain. The call is
// serialised per-wallet via acquireWalletOpsLock and metered per-caller via
// incrDailyQuota, matching the withdrawal path so manual consolidations
// share the same operational envelope.
//
// Quota is keyed on `callerAccountID` — the authenticated caller's account
// — not on the wallet's owning account. For shared wallets this prevents
// one caller from burning the whole account's daily quota. The increment
// is deferred until after the passphrase has successfully decrypted the
// customer share, so a bad passphrase (or any failure in the preceding
// lookup/guard pipeline) never consumes quota.
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
	callerAccountID uuid.UUID,
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
	if !supportedSweepAdapter(chainEntity.AdapterType) {
		return nil, ErrUnsupportedChain
	}

	release, err := s.acquireWalletOpsLock(ctx, walletID)
	if err != nil {
		return nil, err
	}
	defer release()

	limits, err := s.LoadLimits(ctx, callerAccountID)
	if err != nil {
		return nil, err
	}

	adapter, err := s.registry.ChainForWallet(wallet)
	if err != nil {
		return nil, fmt.Errorf("sweep: adapter for %q: %w", wallet.Chain, err)
	}

	plan, err := s.planConsolidation(ctx, adapter, wallet, chainEntity, asset, limits)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return &Result{}, nil
	}

	// Decrypt share A BEFORE incrementing the daily quota. An invalid
	// passphrase or any failure up to this point must not consume quota —
	// otherwise a caller who mistypes their passphrase can lock themselves
	// out of the day's consolidations.
	shareA, err := s.decryptShareA(wallet, passphrase)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(shareA)

	if err := s.incrDailyQuota(ctx, callerAccountID, limits); err != nil {
		return nil, err
	}

	shareB, err := s.fetchShareB(ctx, wallet)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(shareB)

	curve := mpcpkg.Curve(wallet.MPCCurve)
	keys := walletKeys{shareA: shareA, shareB: shareB, passphrase: passphrase}
	result := &Result{EstimatedGas: copyBigInt(plan.EstimatedGas)}

	for i, leg := range plan.Sweeps {
		sweepTxID := uuid.New()
		hash, err := s.broadcastLeg(
			ctx, adapter, curve, keys, wallet, plan, leg, sweepTxID,
			legBroadcastOpts{
				Origin:              models.TxOriginManualConsolidation,
				ParentTransactionID: nil,
			},
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

// planConsolidation is the multi-sweep plan that moves every eligible child balance
// of asset to the base address, or nil when no child holds a sweepable amount.
func (s *service) planConsolidation(
	ctx context.Context,
	adapter types.Chain,
	wallet *models.Wallet,
	chainEntity *models.Chain,
	asset string,
	limits *Limits,
) (*Plan, error) {
	children, err := s.addressRepo.FindByWalletID(wallet.ID)
	if err != nil {
		return nil, fmt.Errorf("sweep: list children: %w", err)
	}

	dust := s.childDustThreshold(ctx, adapter, chainEntity, asset)
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
		return nil, nil
	}

	if err := checkAddressesPerRequest(chainEntity.AdapterType, len(eligible), limits); err != nil {
		return nil, err
	}

	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].balance.Cmp(eligible[j].balance) > 0
	})

	reserve, err := loadNativeReserve(ctx, adapter, asset)
	if err != nil {
		return nil, err
	}
	totalAmount := new(big.Int)
	sweeps := make([]PlannedSweep, 0, len(eligible))
	for _, cb := range eligible {
		swept := reserve.sweepableAmount(cb.balance)
		if swept.Sign() <= 0 {
			continue
		}
		sweeps = append(sweeps, PlannedSweep{
			From:     cb.addr,
			Amount:   swept,
			NeedsGas: chainNeedsGasSeed(wallet.Chain),
		})
		totalAmount.Add(totalAmount, swept)
	}
	if len(sweeps) == 0 {
		return nil, nil
	}
	plan := &Plan{
		WalletID:      wallet.ID,
		Chain:         wallet.Chain,
		Asset:         asset,
		Amount:        totalAmount,
		Strategy:      StrategyMultiSweep,
		Sweeps:        sweeps,
		ReachesTarget: true,
	}
	plan.EstimatedGas, err = s.estimatePlanGas(ctx, adapter, plan, gasPlanTarget{
		baseAddress: wallet.DepositAddress.Address,
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
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
