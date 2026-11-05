package ramp

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// oneHundred bounds a fee percent and scales it to basis points.
var oneHundred = big.NewRat(100, 1)

// feePercentPattern is a plain decimal with an optional sign. It is stricter
// than big.Rat.SetString, which also takes "1_0", "3/4", "1e2" and "0x1p-2",
// each a plausible number nobody typed as a fee; and looser than
// decimalAmount, because this also reads stored values back, and one written
// ".5", "1." or "+1.5" must keep quoting.
var feePercentPattern = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)$`)

// FeePercentToSpreadBps converts a fee percent as a rail's settings group
// stores it ("1.50") into integer basis points (150), which is how some
// providers express the same thing. It is also the fee_percent save validator.
//
// The arithmetic is exact: big.Rat, never float64. A fee is money, and
// 1.15 * 100 in binary floating point is 114.99999999999999, which truncates to
// 114 bps and quietly under-charges every order on that rail.
//
// Rounding is half-up, so a percent that lands between two basis points always
// resolves the same way rather than depending on the value's parity.
func FeePercentToSpreadBps(feePercent string) (int, error) {
	trimmed := strings.TrimSpace(feePercent)
	if trimmed == "" {
		return 0, errors.New("fee percent is empty")
	}
	if !feePercentPattern.MatchString(trimmed) {
		return 0, fmt.Errorf("fee percent %q is not a decimal", feePercent)
	}

	percent, ok := new(big.Rat).SetString(trimmed)
	if !ok {
		return 0, fmt.Errorf("fee percent %q is not a number", feePercent)
	}
	if percent.Sign() < 0 {
		return 0, fmt.Errorf("fee percent %q is negative", feePercent)
	}
	if percent.Cmp(oneHundred) > 0 {
		return 0, fmt.Errorf("fee percent %q exceeds 100", feePercent)
	}

	bps := new(big.Rat).Mul(percent, oneHundred)
	// Half-up: add ½ then take the floor. Both operands are non-negative here,
	// so integer truncation is the floor.
	bps.Add(bps, big.NewRat(1, 2))
	return int(new(big.Int).Quo(bps.Num(), bps.Denom()).Int64()), nil
}
