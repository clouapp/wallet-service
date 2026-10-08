package models

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// Bounds of a wallet's fee settings. The multiplier scales the network fee the
// adapters quote (EVM gas price, Bitcoin fee rate): below 1 a withdrawal would
// bid under the network estimate and could stall, above 5 one fee setting could
// overpay by an order of magnitude (EVM already bids 2 × eth_gasPrice).
var (
	FeeMultiplierMin = decimal.NewFromInt(1)
	FeeMultiplierMax = decimal.NewFromInt(5)
)

// Bitcoin fee-rate bounds, in sat/vB: 1 is the default min relay fee and 10 000
// is the rate above which the adapter rejects an estimator answer as garbage.
const (
	FeeRateMinSatPerVByte = 1
	FeeRateMaxSatPerVByte = 10_000
)

var (
	// ErrFeeMultiplierOutOfRange marks a multiplier outside [FeeMultiplierMin, FeeMultiplierMax].
	ErrFeeMultiplierOutOfRange = errors.New("fee_multiplier is outside the allowed range")
	// ErrFeeRateOutOfRange marks a sat/vB bound outside [FeeRateMinSatPerVByte, FeeRateMaxSatPerVByte].
	ErrFeeRateOutOfRange = errors.New("fee rate is outside the allowed range")
	// ErrFeeRateBoundsInverted marks fee_rate_min above fee_rate_max.
	ErrFeeRateBoundsInverted = errors.New("fee_rate_min must not exceed fee_rate_max")
)

// ValidateFeeMultiplier accepts a multiplier that fits numeric(8,4) exactly and
// lies in [FeeMultiplierMin, FeeMultiplierMax].
func ValidateFeeMultiplier(multiplier decimal.Decimal) error {
	if err := FeeMultiplierColumn.Validate(multiplier); err != nil {
		return err
	}
	if multiplier.LessThan(FeeMultiplierMin) || multiplier.GreaterThan(FeeMultiplierMax) {
		return fmt.Errorf("%w: %s not in [%s, %s]", ErrFeeMultiplierOutOfRange,
			multiplier.String(), FeeMultiplierMin.String(), FeeMultiplierMax.String())
	}
	return nil
}

// ValidateFeeRateBounds checks optional sat/vB bounds: each in range, min ≤ max.
func ValidateFeeRateBounds(minimum, maximum *int) error {
	for _, bound := range []struct {
		name  string
		value *int
	}{{"fee_rate_min", minimum}, {"fee_rate_max", maximum}} {
		if bound.value == nil {
			continue
		}
		if *bound.value < FeeRateMinSatPerVByte || *bound.value > FeeRateMaxSatPerVByte {
			return fmt.Errorf("%w: %s %d not in [%d, %d] sat/vB", ErrFeeRateOutOfRange,
				bound.name, *bound.value, FeeRateMinSatPerVByte, FeeRateMaxSatPerVByte)
		}
	}
	if minimum != nil && maximum != nil && *minimum > *maximum {
		return fmt.Errorf("%w: %d > %d", ErrFeeRateBoundsInverted, *minimum, *maximum)
	}
	return nil
}

// FeeMultiplierApplies reports whether the chain prices fees per unit, so a
// multiplier changes them (EVM gas price, Bitcoin fee rate). Solana fees are a
// flat 5000 lamports per signature.
func FeeMultiplierApplies(adapterType string) bool {
	return adapterType == AdapterTypeEVM || adapterType == AdapterTypeBitcoin
}

// FeeRateBoundsApply reports whether sat/vB bounds mean anything for the chain.
func FeeRateBoundsApply(adapterType string) bool {
	return adapterType == AdapterTypeBitcoin
}
