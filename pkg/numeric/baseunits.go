package numeric

import (
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"
)

// MaxBaseUnitDecimals bounds the decimals of an asset (EVM tokens use at most 36 in
// practice; native coins 8 to 18).
const MaxBaseUnitDecimals = 36

// ToBaseUnits converts a human amount (1.5 ETH) to integer base units (amount ×
// 10^decimals), rounding half away from zero to a whole unit. A real on-chain amount
// never has more than `decimals` places, so the rounding only absorbs noise in a
// provider's JSON number. Negative amounts are rejected.
func ToBaseUnits(human decimal.Decimal, decimals int32) (*big.Int, error) {
	if decimals < 0 || decimals > MaxBaseUnitDecimals {
		return nil, fmt.Errorf("decimals %d out of range 0..%d", decimals, MaxBaseUnitDecimals)
	}
	if human.IsNegative() {
		return nil, fmt.Errorf("amount %s: %w", human.String(), ErrNegative)
	}
	return human.Shift(decimals).Round(0).BigInt(), nil
}
