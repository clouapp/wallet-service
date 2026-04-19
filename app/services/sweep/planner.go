package sweep

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const reasonBelowDustThreshold = "below_dust_threshold"

// Per-transaction gas limits used by the EVM adapter. Kept in sync with
// EVMLive.BuildTransfer / BuildSweep (21_000 for a native transfer; 65_000
// for an ERC-20 transfer) so the estimate reflects what execution will
// actually emit. If those constants drift, this estimate will drift too.
const (
	gasLimitNativeTransfer = 21_000
	gasLimitERC20Transfer  = 65_000
)

// PlanForWithdrawal chooses the cheapest strategy to cover `amount` of `asset`
// from `walletID`. See docs/superpowers/plans/2026-04-18-base-address-sweep.md
// §4.1 for the full algorithm.
//
// v1 scope: EVM-only. Non-EVM wallets return ErrUnsupportedChain.
func (s *service) PlanForWithdrawal(ctx context.Context, walletID uuid.UUID, asset string, amount *big.Int) (*Plan, error) {
	if amount == nil {
		return nil, fmt.Errorf("sweep: amount must not be nil")
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

	adapter, err := s.registry.Chain(wallet.Chain)
	if err != nil {
		return nil, fmt.Errorf("sweep: adapter not registered for %q: %w", wallet.Chain, err)
	}

	baseBal, err := fetchBalance(ctx, s.registry, wallet.Chain, adapter, *wallet.DepositAddress, asset)
	if err != nil {
		return nil, fmt.Errorf("sweep: base balance: %w", err)
	}

	plan := &Plan{
		WalletID:    walletID,
		Chain:       wallet.Chain,
		Asset:       asset,
		Amount:      new(big.Int).Set(amount),
		BaseBalance: baseBal,
	}

	// 1) direct_from_base — base alone covers the full amount.
	if baseBal.Cmp(amount) >= 0 {
		plan.Strategy = StrategyDirectFromBase
		baseCopy := *wallet.DepositAddress
		plan.SourceAddress = &baseCopy
		plan.ReachesTarget = true
		plan.EstimatedGas = estimatePlanGas(ctx, adapter, plan)
		return plan, nil
	}

	// 2) Collect eligible children (balance > 0, not base, above dust threshold).
	children, err := s.addressRepo.FindByWalletID(walletID)
	if err != nil {
		return nil, fmt.Errorf("sweep: list children: %w", err)
	}

	dust := adapter.DustThreshold(asset) // nil → no dust filtering
	type childBal struct {
		addr    models.Address
		balance *big.Int
	}
	eligible := make([]childBal, 0, len(children))
	dustIgnored := make([]AddressBalance, 0)

	for _, c := range children {
		if c.ID == wallet.DepositAddress.ID {
			continue
		}
		bal, err := fetchBalance(ctx, s.registry, wallet.Chain, adapter, c, asset)
		if err != nil {
			return nil, fmt.Errorf("sweep: child balance %s: %w", c.Address, err)
		}
		if bal == nil || bal.Sign() == 0 {
			continue
		}
		if dust != nil && dust.Sign() > 0 && bal.Cmp(dust) < 0 {
			dustIgnored = append(dustIgnored, AddressBalance{
				Address: c,
				Balance: new(big.Int).Set(bal),
				Reason:  reasonBelowDustThreshold,
			})
			continue
		}
		eligible = append(eligible, childBal{addr: c, balance: new(big.Int).Set(bal)})
	}
	plan.DustIgnored = dustIgnored

	// 3) direct_from_child — one child alone covers the amount.
	for _, cb := range eligible {
		if cb.balance.Cmp(amount) >= 0 {
			plan.Strategy = StrategyDirectFromChild
			addrCopy := cb.addr
			plan.SourceAddress = &addrCopy
			plan.ReachesTarget = true
			plan.EstimatedGas = estimatePlanGas(ctx, adapter, plan)
			return plan, nil
		}
	}

	// 4) multi_sweep — greedy, largest balance first.
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].balance.Cmp(eligible[j].balance) > 0
	})

	remaining := new(big.Int).Sub(amount, baseBal)
	total := new(big.Int).Set(baseBal)
	sweeps := make([]PlannedSweep, 0, len(eligible))
	for _, cb := range eligible {
		if remaining.Sign() <= 0 {
			break
		}
		sweeps = append(sweeps, PlannedSweep{
			From:     cb.addr,
			Amount:   new(big.Int).Set(cb.balance),
			NeedsGas: chainNeedsGasSeed(wallet.Chain),
		})
		total.Add(total, cb.balance)
		remaining.Sub(remaining, cb.balance)
	}

	if total.Cmp(amount) < 0 {
		plan.Strategy = StrategyInsufficient
		plan.ReachesTarget = false
		return plan, nil
	}

	plan.Strategy = StrategyMultiSweep
	plan.Sweeps = sweeps
	plan.ReachesTarget = true
	plan.EstimatedGas = estimatePlanGas(ctx, adapter, plan)
	return plan, nil
}

// estimatePlanGas fetches the current gas price from the adapter and applies
// the per-strategy gas-limit model encoded in estimateGasTotal. A nil return
// means the estimate is unavailable (non-EVM chain, gas-price fetch failed,
// or strategy has no associated gas cost); callers must tolerate a nil.
func estimatePlanGas(ctx context.Context, adapter types.Chain, plan *Plan) *big.Int {
	if plan == nil {
		return nil
	}
	gasPrice, err := adapter.EstimateGasPrice(ctx)
	if err != nil || gasPrice == nil {
		return nil
	}
	return estimateGasTotal(gasPrice, plan, plan.Asset != adapter.NativeAsset())
}

// estimateGasTotal is a pure helper: given a gas price, a plan, and whether
// the target asset is an ERC-20 token, it returns the total native-unit gas
// cost the executor will consume end-to-end.
//
// Gas-limit model (matches EVMLive.BuildTransfer + BuildSweep):
//   - native transfer: gasLimitNativeTransfer
//   - ERC-20 transfer: gasLimitERC20Transfer
//   - per sweep leg with NeedsGas=true: +gasLimitNativeTransfer (gas_seed)
//   - per sweep leg: +native-or-ERC-20 sweep limit
//   - final withdrawal (multi_sweep / direct_*): +native-or-ERC-20 limit
//
// Returns nil when the inputs make an estimate impossible (nil gasPrice,
// nil plan, or insufficient / unknown strategy).
func estimateGasTotal(gasPrice *big.Int, plan *Plan, isTokenSweep bool) *big.Int {
	if gasPrice == nil || plan == nil {
		return nil
	}

	finalLimit := uint64(gasLimitNativeTransfer)
	if isTokenSweep {
		finalLimit = gasLimitERC20Transfer
	}

	switch plan.Strategy {
	case StrategyDirectFromBase, StrategyDirectFromChild:
		return new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(finalLimit))

	case StrategyMultiSweep:
		totalGas := uint64(0)
		for _, leg := range plan.Sweeps {
			if leg.NeedsGas {
				totalGas += gasLimitNativeTransfer
			}
			if isTokenSweep {
				totalGas += gasLimitERC20Transfer
			} else {
				totalGas += gasLimitNativeTransfer
			}
		}
		totalGas += finalLimit
		return new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(totalGas))

	default:
		return nil
	}
}

// fetchBalance returns the raw big.Int balance of `addr` for `asset`.
// When asset matches the adapter's native symbol, it calls GetBalance;
// otherwise it resolves the token via the registry and calls GetTokenBalance.
func fetchBalance(
	ctx context.Context,
	registry *chain.Registry,
	chainID string,
	adapter types.Chain,
	addr models.Address,
	asset string,
) (*big.Int, error) {
	if asset == adapter.NativeAsset() {
		bal, err := adapter.GetBalance(ctx, addr.Address)
		if err != nil {
			return nil, err
		}
		if bal == nil || bal.Amount == nil {
			return new(big.Int), nil
		}
		return bal.Amount, nil
	}

	token, err := registry.FindToken(chainID, asset)
	if err != nil {
		return nil, fmt.Errorf("token %q not registered on chain %s: %w", asset, chainID, err)
	}
	bal, err := adapter.GetTokenBalance(ctx, addr.Address, *token)
	if err != nil {
		return nil, err
	}
	if bal == nil || bal.Amount == nil {
		return new(big.Int), nil
	}
	return bal.Amount, nil
}

// chainNeedsGasSeed reports whether the chain requires a preparatory gas_seed
// transaction before an ERC-20-style sweep leg. v1 is EVM-only, so this is
// always true when the planner reaches this code path.
func chainNeedsGasSeed(chainID string) bool {
	_ = chainID
	return true
}
