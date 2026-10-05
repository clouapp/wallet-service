package chain

import (
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// FeePolicy is a wallet's adjustment of the network fee its transactions bid:
// the multiplier scales the EVM gas price and the Bitcoin fee rate, and the
// optional sat/vB bounds clamp the Bitcoin rate. The zero value changes nothing.
type FeePolicy struct {
	multiplier          decimal.Decimal
	minMilliSatPerVByte int64
	maxMilliSatPerVByte int64
}

// FeePolicyScoped is implemented by adapters that can price with a FeePolicy;
// the returned adapter shares the original's connections and caches.
type FeePolicyScoped interface {
	WithFeePolicy(policy FeePolicy) types.Chain
}

// FeePolicyForWallet reads the wallet's fee settings. NULL settings are the
// zero policy; stored values outside their bounds are refused, so a corrupted
// row stops the withdrawal instead of overpaying.
func FeePolicyForWallet(wallet *models.Wallet) (FeePolicy, error) {
	if wallet == nil {
		return FeePolicy{}, fmt.Errorf("fee policy: wallet is required")
	}
	policy := FeePolicy{}
	if wallet.FeeMultiplier.Valid {
		if err := models.ValidateFeeMultiplier(wallet.FeeMultiplier.Decimal); err != nil {
			return FeePolicy{}, fmt.Errorf("fee policy of wallet %s: %w", wallet.ID, err)
		}
		if !wallet.FeeMultiplier.Decimal.Equal(models.FeeMultiplierMin) {
			policy.multiplier = wallet.FeeMultiplier.Decimal
		}
	}
	if err := models.ValidateFeeRateBounds(wallet.FeeRateMin, wallet.FeeRateMax); err != nil {
		return FeePolicy{}, fmt.Errorf("fee policy of wallet %s: %w", wallet.ID, err)
	}
	if wallet.FeeRateMin != nil {
		policy.minMilliSatPerVByte = int64(*wallet.FeeRateMin) * milliSatsPerSat
	}
	if wallet.FeeRateMax != nil {
		policy.maxMilliSatPerVByte = int64(*wallet.FeeRateMax) * milliSatsPerSat
	}
	return policy, nil
}

// FeePolicyDeps is the multiplier NewFeePolicy validates. The minimum
// multiplier is the zero policy.
type FeePolicyDeps struct {
	Multiplier decimal.Decimal
}

// NewFeePolicy builds a policy with only a multiplier, validated like a stored one.
func NewFeePolicy(deps FeePolicyDeps) (FeePolicy, error) {
	if err := models.ValidateFeeMultiplier(deps.Multiplier); err != nil {
		return FeePolicy{}, err
	}
	if deps.Multiplier.Equal(models.FeeMultiplierMin) {
		return FeePolicy{}, nil
	}
	return FeePolicy{multiplier: deps.Multiplier}, nil
}

// IsDefault reports whether the policy leaves every network fee unchanged.
func (p FeePolicy) IsDefault() bool {
	return !p.hasMultiplier() && p.minMilliSatPerVByte == 0 && p.maxMilliSatPerVByte == 0
}

// Multiplier is the factor applied to network fees (1 when unset).
func (p FeePolicy) Multiplier() decimal.Decimal {
	if !p.hasMultiplier() {
		return models.FeeMultiplierMin
	}
	return p.multiplier
}

func (p FeePolicy) hasMultiplier() bool {
	return !p.multiplier.IsZero()
}

// scaleGasPrice is ceil(gasPrice × multiplier) in wei; nil and non-positive
// prices pass through so callers keep their own "no usable price" checks.
func (p FeePolicy) scaleGasPrice(gasPrice *big.Int) *big.Int {
	if gasPrice == nil || gasPrice.Sign() <= 0 || !p.hasMultiplier() {
		return gasPrice
	}
	return decimal.NewFromBigInt(gasPrice, 0).Mul(p.multiplier).Ceil().BigInt()
}

// adjustMilliSatRate scales a Bitcoin rate (milli-sat/vB) by the multiplier,
// rounding up, then clamps it to the wallet's bounds and to the adapter's sanity
// ceiling. Rates are never lowered below the min relay fee.
func (p FeePolicy) adjustMilliSatRate(milliSatPerVByte int64) int64 {
	if milliSatPerVByte <= 0 {
		return milliSatPerVByte
	}
	rate := milliSatPerVByte
	if p.hasMultiplier() {
		rate = decimal.NewFromInt(milliSatPerVByte).Mul(p.multiplier).Ceil().IntPart()
	}
	if p.minMilliSatPerVByte > 0 {
		rate = max(rate, p.minMilliSatPerVByte)
	}
	if p.maxMilliSatPerVByte > 0 {
		rate = min(rate, p.maxMilliSatPerVByte)
	}
	rate = min(rate, int64(btcMaxSaneSatPerVByte)*milliSatsPerSat)
	return max(rate, btcMinRelayMilliSatPerVByte)
}

// AdjustMilliSatRate is the Bitcoin rate after this policy. The live Bitcoin
// adapter prices with it.
func (p FeePolicy) AdjustMilliSatRate(milliSatPerVByte int64) int64 {
	return p.adjustMilliSatRate(milliSatPerVByte)
}

// ChainForWallet is the wallet's adapter, pricing fees with the wallet's
// FeePolicy. Adapters without per-unit fees (Solana, test doubles) and wallets
// with default settings get the shared adapter.
func (r *Registry) ChainForWallet(wallet *models.Wallet) (types.Chain, error) {
	if wallet == nil {
		return nil, fmt.Errorf("chain for wallet: wallet is required")
	}
	adapter, err := r.Chain(wallet.Chain)
	if err != nil {
		return nil, err
	}
	policy, err := FeePolicyForWallet(wallet)
	if err != nil {
		return nil, err
	}
	if policy.IsDefault() {
		return adapter, nil
	}
	scoped, ok := adapter.(FeePolicyScoped)
	if !ok {
		return adapter, nil
	}
	return scoped.WithFeePolicy(policy), nil
}

// FeePolicyFingerprint identifies a wallet's fee settings in cache keys: wallets
// with the same fingerprint price identically.
func FeePolicyFingerprint(wallet *models.Wallet) string {
	if wallet == nil {
		return "default"
	}
	policy, err := FeePolicyForWallet(wallet)
	if err != nil {
		return "invalid"
	}
	if policy.IsDefault() {
		return "default"
	}
	return fmt.Sprintf("m%s-min%d-max%d", policy.Multiplier().String(), policy.minMilliSatPerVByte, policy.maxMilliSatPerVByte)
}

// FeePolicyReporter is implemented by adapters that price with a FeePolicy.
type FeePolicyReporter interface {
	FeePolicy() FeePolicy
}

// AppliedFeeMultiplier is the multiplier adapter applies to network fees: 1 for
// adapters without per-unit fees or with the default policy.
func AppliedFeeMultiplier(adapter types.Chain) decimal.Decimal {
	reporter, ok := adapter.(FeePolicyReporter)
	if !ok {
		return models.FeeMultiplierMin
	}
	return reporter.FeePolicy().Multiplier()
}
