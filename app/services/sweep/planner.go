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

// Fallback per-transaction gas limits for adapters that do not implement
// types.TransferGasEstimator. EVMLive implements it, so EVM plans are sized
// with the same limits BuildTransfer / BuildSweep encode.
const (
	gasLimitNativeTransfer = 21_000
	gasLimitERC20Transfer  = 65_000
)

// PlanForWithdrawal chooses the cheapest strategy to cover `amount` of `asset`
// from `walletID`. See docs/superpowers/plans/2026-04-18-base-address-sweep.md
// §4.1 for the full algorithm.
//
// `callerAccountID` keys the per-request address-cap lookup on the
// authenticated caller's account so per-caller overrides apply correctly on
// shared wallets. Pass uuid.Nil from non-authenticated contexts (tests) to
// fall back to system defaults.
//
// `toAddress` is the withdrawal recipient. It is needed to size token
// transfers; pass "" when it is unknown (preview), which leaves a token plan's
// EstimatedGas nil. A transfer the node cannot estimate (would revert) fails
// the plan with chain.ErrGasEstimateFailed before anything is broadcast.
//
// EVM, Solana, and Bitcoin wallets can be planned. Any other adapter returns ErrUnsupportedChain.
func (s *service) PlanForWithdrawal(ctx context.Context, walletID uuid.UUID, asset string, amount *big.Int, toAddress string, callerAccountID uuid.UUID) (*Plan, error) {
	if amount == nil {
		return nil, fmt.Errorf("sweep: amount must not be nil")
	}

	wallet, err := s.walletRepo.FindByID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("sweep: find wallet: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("sweep: wallet %s not found", walletID)
	}
	if wallet.DepositAddress == nil {
		return nil, fmt.Errorf("sweep: wallet %s has no base deposit address", walletID)
	}

	chainEntity, err := s.loadChain(ctx, wallet.Chain)
	if err != nil {
		return nil, fmt.Errorf("sweep: find chain %q: %w", wallet.Chain, err)
	}
	if chainEntity == nil {
		return nil, fmt.Errorf("sweep: chain %q not found", wallet.Chain)
	}
	if !supportedSweepAdapter(chainEntity.AdapterType) {
		return nil, ErrUnsupportedChain
	}

	adapter, err := s.registry.ChainForWallet(wallet)
	if err != nil {
		return nil, fmt.Errorf("sweep: adapter for %q: %w", wallet.Chain, err)
	}

	reserve, err := loadNativeReserve(ctx, adapter, asset)
	if err != nil {
		return nil, err
	}

	base, err := loadSourceFunds(ctx, s.registry, wallet.Chain, adapter, *wallet.DepositAddress, asset, reserve)
	if err != nil {
		return nil, fmt.Errorf("sweep: base balance: %w", err)
	}
	baseBal := base.balance
	requiredAtBase := base.reserve.requiredBalance(amount)

	plan := &Plan{
		WalletID:    walletID,
		Chain:       wallet.Chain,
		Asset:       asset,
		Amount:      new(big.Int).Set(amount),
		BaseBalance: baseBal,
	}
	gasTarget := gasPlanTarget{
		baseAddress:          wallet.DepositAddress.Address,
		toAddress:            toAddress,
		includeFinalTransfer: true,
	}

	// 1) direct_from_base — base alone covers the full amount.
	if baseBal.Cmp(requiredAtBase) >= 0 {
		plan.Strategy = StrategyDirectFromBase
		baseCopy := *wallet.DepositAddress
		plan.SourceAddress = &baseCopy
		plan.ReachesTarget = true
		return s.withEstimatedGas(ctx, adapter, plan, gasTarget)
	}

	// 2) Collect eligible children (balance > 0, not base, above dust threshold).
	children, err := s.addressRepo.FindByWalletID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("sweep: list children: %w", err)
	}

	// Enforce the per-request address cap before issuing N sequential balance
	// RPCs. This protects the planner against pathological wallets (many
	// children) blowing up /withdraw/preview latency and per-node RPC budget.
	// Limits are keyed on the caller's account so per-caller overrides apply
	// on shared wallets; uuid.Nil short-circuits LoadLimits to defaults.
	// LoadLimits never returns an error, but tolerate a nil Limits just in case.
	limits, _ := s.LoadLimits(ctx, callerAccountID)
	if limits != nil {
		if err := checkAddressesPerRequest(chainEntity.AdapterType, len(children), limits); err != nil {
			return nil, err
		}
	}

	dust := s.childDustThreshold(ctx, adapter, chainEntity, asset) // nil → no dust filtering
	type childBal struct {
		addr    models.Address
		balance *big.Int
		reserve nativeReserve
	}
	eligible := make([]childBal, 0, len(children))
	dustIgnored := make([]AddressBalance, 0)

	for _, c := range children {
		if c.ID == wallet.DepositAddress.ID {
			continue
		}
		funds, err := loadSourceFunds(ctx, s.registry, wallet.Chain, adapter, c, asset, reserve)
		if err != nil {
			return nil, fmt.Errorf("sweep: child balance %s: %w", c.Address, err)
		}
		bal := funds.balance
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
		eligible = append(eligible, childBal{addr: c, balance: new(big.Int).Set(bal), reserve: funds.reserve})
	}
	plan.DustIgnored = dustIgnored

	// 3) direct_from_child — one child alone covers the amount.
	for _, cb := range eligible {
		if cb.balance.Cmp(cb.reserve.requiredBalance(amount)) >= 0 {
			plan.Strategy = StrategyDirectFromChild
			addrCopy := cb.addr
			plan.SourceAddress = &addrCopy
			plan.ReachesTarget = true
			return s.withEstimatedGas(ctx, adapter, plan, gasTarget)
		}
	}

	// 4) multi_sweep — greedy, largest balance first.
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].balance.Cmp(eligible[j].balance) > 0
	})

	remaining := new(big.Int).Sub(requiredAtBase, baseBal)
	total := new(big.Int).Set(baseBal)
	sweeps := make([]PlannedSweep, 0, len(eligible))
	for _, cb := range eligible {
		if remaining.Sign() <= 0 {
			break
		}
		swept := cb.reserve.sweepableAmount(cb.balance)
		if swept.Sign() <= 0 {
			continue
		}
		sweeps = append(sweeps, PlannedSweep{
			From:     cb.addr,
			Amount:   swept,
			NeedsGas: chainNeedsGasSeed(wallet.Chain),
		})
		total.Add(total, swept)
		remaining.Sub(remaining, swept)
	}

	if total.Cmp(requiredAtBase) < 0 {
		plan.Strategy = StrategyInsufficient
		plan.ReachesTarget = false
		return plan, nil
	}

	plan.Strategy = StrategyMultiSweep
	plan.Sweeps = sweeps
	plan.ReachesTarget = true
	return s.withEstimatedGas(ctx, adapter, plan, gasTarget)
}

// gasPlanTarget names the addresses a plan's transfers move funds between so
// the estimate sizes the same transactions the executor will build.
type gasPlanTarget struct {
	baseAddress string
	// toAddress is the withdrawal recipient; "" when unknown (preview).
	toAddress string
	// includeFinalTransfer is false for consolidation, which only sweeps into base.
	includeFinalTransfer bool
}

func (s *service) withEstimatedGas(ctx context.Context, adapter types.Chain, plan *Plan, target gasPlanTarget) (*Plan, error) {
	estimated, err := s.estimatePlanGas(ctx, adapter, plan, target)
	if err != nil {
		return nil, err
	}
	plan.EstimatedGas = estimated
	return plan, nil
}

// estimatePlanGas returns the native gas the plan can spend. Adapters that
// implement types.TransferGasEstimator are asked for each transfer's limit, so
// the total matches what BuildTransfer / BuildSweep encode; others fall back to
// the fixed model in estimateGasTotal. A nil total means unavailable (no gas
// price, unknown recipient for a token transfer, or a strategy without gas
// cost). An error means a transfer cannot be sized, e.g. it would revert.
func (s *service) estimatePlanGas(ctx context.Context, adapter types.Chain, plan *Plan, target gasPlanTarget) (*big.Int, error) {
	if plan == nil {
		return nil, nil
	}
	estimator, canEstimate := adapter.(types.TransferGasEstimator)
	if !canEstimate {
		gasPrice, err := adapter.EstimateGasPrice(ctx)
		if err != nil || gasPrice == nil {
			return nil, nil
		}
		return estimateGasTotal(gasPrice, plan, !types.SameAssetSymbol(plan.Asset, adapter.NativeAsset())), nil
	}

	token, err := s.planToken(adapter, plan)
	if err != nil {
		return nil, err
	}
	gasUnits, known, err := estimatedPlanGasUnits(ctx, estimator, plan, token, target)
	if err != nil {
		return nil, err
	}
	gasPrice, err := adapter.EstimateGasPrice(ctx)
	if !known || err != nil || gasPrice == nil {
		return nil, nil
	}
	total := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasUnits))
	l1Fees, err := planL1DataFees(ctx, adapter, plan, token, target, gasPrice)
	if err != nil {
		return nil, err
	}
	return total.Add(total, l1Fees), nil
}

// gasSeedLimiter is implemented by chains whose gas_seed (a native transfer from
// base) can need more than gasLimitNativeTransfer (Arbitrum bills L1 cost as gas).
type gasSeedLimiter interface {
	NativeTransferGasLimit(ctx context.Context, from, to string) (uint64, error)
}

func gasSeedGasLimit(ctx context.Context, estimator types.TransferGasEstimator, from, to string) (uint64, error) {
	limiter, ok := estimator.(gasSeedLimiter)
	if !ok {
		return gasLimitNativeTransfer, nil
	}
	return limiter.NativeTransferGasLimit(ctx, from, to)
}

// planL1DataFees sums the L1 data fee of every transfer the executor will send for
// plan; zero for adapters that charge none.
func planL1DataFees(
	ctx context.Context,
	adapter types.Chain,
	plan *Plan,
	token *types.Token,
	target gasPlanTarget,
	gasPrice *big.Int,
) (*big.Int, error) {
	total := new(big.Int)
	estimator, ok := adapter.(types.L1DataFeeEstimator)
	if !ok {
		return total, nil
	}
	for _, req := range planTransfers(plan, token, target) {
		req.GasPrice = gasPrice
		fee, err := estimator.EstimateL1DataFee(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("sweep: estimate L1 data fee from %s: %w", req.From, err)
		}
		if fee == nil || fee.Sign() < 0 {
			return nil, fmt.Errorf("sweep: chain %s returned an invalid L1 data fee", adapter.ID())
		}
		total.Add(total, fee)
	}
	return total, nil
}

// planTransfers lists the transfers the executor sends for plan, in order: per
// sweep leg an optional gas_seed (base → child) and the sweep (child → base), then
// the withdrawal itself.
func planTransfers(plan *Plan, token *types.Token, target gasPlanTarget) []types.TransferRequest {
	transfers := make([]types.TransferRequest, 0, 2*len(plan.Sweeps)+1)
	final := func(from string) types.TransferRequest {
		return planTransferRequest(plan, token, from, target.toAddress, plan.Amount)
	}
	switch plan.Strategy {
	case StrategyDirectFromBase, StrategyDirectFromChild:
		if plan.SourceAddress != nil {
			transfers = append(transfers, final(plan.SourceAddress.Address))
		}
	case StrategyMultiSweep:
		for _, leg := range plan.Sweeps {
			if leg.NeedsGas {
				transfers = append(transfers, types.TransferRequest{From: target.baseAddress, To: leg.From.Address})
			}
			transfers = append(transfers, planTransferRequest(plan, token, leg.From.Address, target.baseAddress, leg.Amount))
		}
		if target.includeFinalTransfer {
			transfers = append(transfers, final(target.baseAddress))
		}
	}
	return transfers
}

func (s *service) planToken(adapter types.Chain, plan *Plan) (*types.Token, error) {
	if types.SameAssetSymbol(plan.Asset, adapter.NativeAsset()) {
		return nil, nil
	}
	token, err := s.registry.FindToken(plan.Chain, plan.Asset)
	if err != nil {
		return nil, fmt.Errorf("sweep: token %q not registered on chain %s: %w", plan.Asset, plan.Chain, err)
	}
	return token, nil
}

// estimatedPlanGasUnits sums the gas limits of every transfer the executor
// will send for plan. The bool is false when the total cannot be known.
func estimatedPlanGasUnits(
	ctx context.Context,
	estimator types.TransferGasEstimator,
	plan *Plan,
	token *types.Token,
	target gasPlanTarget,
) (uint64, bool, error) {
	switch plan.Strategy {
	case StrategyDirectFromBase, StrategyDirectFromChild:
		if plan.SourceAddress == nil {
			return 0, false, nil
		}
		return estimateFinalTransferGas(ctx, estimator, plan, token, plan.SourceAddress.Address, target)

	case StrategyMultiSweep:
		var total uint64
		for _, leg := range plan.Sweeps {
			if leg.NeedsGas {
				seedLimit, err := gasSeedGasLimit(ctx, estimator, target.baseAddress, leg.From.Address)
				if err != nil {
					return 0, false, fmt.Errorf("sweep: estimate gas_seed to %s: %w", leg.From.Address, err)
				}
				total += seedLimit
			}
			limit, err := estimator.EstimateTransferGasLimit(ctx, planTransferRequest(plan, token, leg.From.Address, target.baseAddress, leg.Amount))
			if err != nil {
				return 0, false, fmt.Errorf("sweep: estimate gas for sweep from %s: %w", leg.From.Address, err)
			}
			total += limit
		}
		if !target.includeFinalTransfer {
			return total, true, nil
		}
		final, known, err := estimateFinalTransferGas(ctx, estimator, plan, token, target.baseAddress, target)
		if err != nil || !known {
			return 0, false, err
		}
		return total + final, true, nil

	default:
		return 0, false, nil
	}
}

func estimateFinalTransferGas(
	ctx context.Context,
	estimator types.TransferGasEstimator,
	plan *Plan,
	token *types.Token,
	from string,
	target gasPlanTarget,
) (uint64, bool, error) {
	if token != nil && target.toAddress == "" {
		return 0, false, nil
	}
	limit, err := estimator.EstimateTransferGasLimit(ctx, planTransferRequest(plan, token, from, target.toAddress, plan.Amount))
	if err != nil {
		return 0, false, fmt.Errorf("sweep: estimate gas for withdrawal from %s: %w", from, err)
	}
	return limit, true, nil
}

func planTransferRequest(plan *Plan, token *types.Token, from, to string, amount *big.Int) types.TransferRequest {
	return types.TransferRequest{
		From:   from,
		To:     to,
		Amount: amount,
		Asset:  plan.Asset,
		Token:  token,
	}
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
	if types.SameAssetSymbol(asset, adapter.NativeAsset()) {
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
// transaction before a token sweep. SOL and BTC pay fees from the source
// itself and do not receive an EVM gas top-up.
func chainNeedsGasSeed(chainID string) bool {
	switch chainID {
	case models.ChainSOL, models.ChainTSOL, models.ChainBTC, models.ChainTBTC:
		return false
	default:
		return true
	}
}

func supportedSweepAdapter(adapterType string) bool {
	switch adapterType {
	case models.AdapterTypeEVM, models.AdapterTypeSolana, models.AdapterTypeBitcoin:
		return true
	default:
		return false
	}
}
