package sweep

import (
	"context"
	"fmt"
	"math/big"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// evmTokenGasApplies uses the EVM recipient-specific native-fee port. Other
// adapters (including TRON's energy estimator) keep their own funding rules.
func evmTokenGasApplies(adapter types.Chain, asset string) bool {
	_, evm := adapter.(nativeRecipientReserver)
	return evm && !types.SameAssetSymbol(asset, adapter.NativeAsset())
}

func nativeGasBalance(ctx context.Context, adapter types.Chain, address string) (*big.Int, error) {
	balance, err := adapter.GetBalance(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("sweep: native gas balance: %w", err)
	}
	if balance == nil || balance.Amount == nil || balance.Amount.Sign() < 0 {
		return nil, fmt.Errorf("sweep: native gas balance is unavailable")
	}
	return new(big.Int).Set(balance.Amount), nil
}

// tokenTransferNativeFee prices the same gas limit, wallet-specific gas price
// and rollup L1 fee as BuildTransfer. No estimate may silently become zero.
func tokenTransferNativeFee(ctx context.Context, adapter types.Chain, req types.TransferRequest) (*big.Int, error) {
	estimator, ok := adapter.(types.TransferGasEstimator)
	if !ok {
		return nil, fmt.Errorf("%w: chain cannot size a token transfer", ErrFeeQuoteUnavailable)
	}
	limit, err := estimator.EstimateTransferGasLimit(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%w: token transfer gas: %w", ErrGasEstimateFailed, err)
	}
	if limit == 0 {
		return nil, fmt.Errorf("%w: token transfer returned no gas limit", ErrGasEstimateFailed)
	}
	price, err := adapter.EstimateGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: gas price: %w", ErrFeeQuoteUnavailable, err)
	}
	if price == nil || price.Sign() <= 0 {
		return nil, fmt.Errorf("%w: no usable gas price", ErrFeeQuoteUnavailable)
	}
	fee := new(big.Int).Mul(price, new(big.Int).SetUint64(limit))
	if l1, ok := adapter.(types.L1DataFeeEstimator); ok {
		req.GasPrice = price
		req.GasLimit = &limit
		dataFee, err := l1.EstimateL1DataFee(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("%w: L1 data fee: %w", ErrFeeQuoteUnavailable, err)
		}
		if dataFee == nil || dataFee.Sign() < 0 {
			return nil, fmt.Errorf("%w: invalid L1 data fee", ErrFeeQuoteUnavailable)
		}
		fee.Add(fee, dataFee)
	}
	return fee, nil
}

func (s *service) tokenSourceGasReady(ctx context.Context, adapter types.Chain, plan *Plan, source, to string) (bool, error) {
	if !evmTokenGasApplies(adapter, plan.Asset) {
		return true, nil
	}
	token, err := s.planToken(adapter, plan)
	if err != nil {
		return false, err
	}
	if to == "" {
		to = chain.EVMFeeProbeRecipient()
	}
	fee, err := tokenTransferNativeFee(ctx, adapter, planTransferRequest(plan, token, source, to, plan.Amount))
	if err != nil {
		return false, err
	}
	balance, err := nativeGasBalance(ctx, adapter, source)
	if err != nil {
		return false, err
	}
	return balance.Cmp(fee) >= 0, nil
}

// requireTokenPlanGas is a read-only precondition before any key retrieval or
// signing. A preparation plan must fund every gas seed (including the builder's
// buffer), its fees and the final transfer. An unfunded base token transfer
// that the node cannot estimate is refused, never priced from a fictitious state.
func (s *service) requireTokenPlanGas(ctx context.Context, adapter types.Chain, wallet *models.Wallet, plan *Plan, to string) error {
	if !evmTokenGasApplies(adapter, plan.Asset) {
		return nil
	}
	if plan.Strategy == StrategyDirectFromBase || plan.Strategy == StrategyDirectFromChild {
		if plan.SourceAddress == nil {
			return fmt.Errorf("sweep: token source address is required")
		}
		ready, err := s.tokenSourceGasReady(ctx, adapter, plan, plan.SourceAddress.Address, to)
		if err != nil {
			return err
		}
		if !ready {
			return ErrWalletNotGasReady
		}
		return nil
	}
	if plan.Strategy != StrategyMultiSweep {
		return nil
	}
	token, err := s.planToken(adapter, plan)
	if err != nil {
		return err
	}
	base := wallet.DepositAddress.Address
	if to == "" {
		to = chain.EVMFeeProbeRecipient()
	}
	baseRequired, err := tokenTransferNativeFee(ctx, adapter, planTransferRequest(plan, token, base, to, plan.Amount))
	if err != nil {
		return err
	}
	for _, leg := range plan.Sweeps {
		fee, feeErr := tokenTransferNativeFee(ctx, adapter, planTransferRequest(plan, token, leg.From.Address, base, leg.Amount))
		if feeErr != nil {
			return feeErr
		}
		balance, balanceErr := nativeGasBalance(ctx, adapter, leg.From.Address)
		if balanceErr != nil {
			return balanceErr
		}
		if balance.Cmp(fee) >= 0 {
			continue
		}
		seed := chain.EVMGasSeedAmount(fee)
		seedFee, seedErr := tokenTransferNativeFee(ctx, adapter, types.TransferRequest{From: base, To: leg.From.Address, Amount: seed, Asset: adapter.NativeAsset()})
		if seedErr != nil {
			return seedErr
		}
		baseRequired.Add(baseRequired, seed)
		baseRequired.Add(baseRequired, seedFee)
	}
	baseBalance, err := nativeGasBalance(ctx, adapter, base)
	if err != nil {
		return err
	}
	if baseBalance.Cmp(baseRequired) < 0 {
		return ErrWalletNotGasReady
	}
	return nil
}
