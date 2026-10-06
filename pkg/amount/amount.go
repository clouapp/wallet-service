// Package amount converts between integer base units (wei, satoshi, lamport, token
// base units) and human decimal strings with exact integer arithmetic.
package amount

import (
	"fmt"
	"math/big"
	"strings"
)

// FormatBaseUnits renders an integer amount of base units as a decimal string
// without trailing zeros (3000000 with 6 decimals is "3"). Nil and negative
// decimals render as "" and the plain integer respectively.
func FormatBaseUnits(baseUnits *big.Int, decimals int) string {
	if baseUnits == nil {
		return ""
	}
	sign := ""
	digits := baseUnits.String()
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if decimals <= 0 {
		return sign + digits
	}
	if len(digits) <= decimals {
		digits = strings.Repeat("0", decimals-len(digits)+1) + digits
	}
	whole := digits[:len(digits)-decimals]
	fraction := strings.TrimRight(digits[len(digits)-decimals:], "0")
	if fraction == "" {
		return sign + whole
	}
	return sign + whole + "." + fraction
}

// NormalizeDecimal strips trailing fractional zeros from a stored decimal ("3.000" is "3").
func NormalizeDecimal(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.Contains(trimmed, ".") {
		return trimmed
	}
	trimmed = strings.TrimRight(trimmed, "0")
	return strings.TrimSuffix(trimmed, ".")
}

// ParseBaseUnits reads a non-negative integer amount of base units.
func ParseBaseUnits(value string) (*big.Int, bool) {
	units, ok := new(big.Int).SetString(strings.TrimSpace(value), 10)
	if !ok || units.Sign() < 0 {
		return nil, false
	}
	return units, true
}

// DecimalToBaseUnits converts a non-negative decimal string into integer base
// units (value × 10^decimals). Extra fractional digits are rejected, so a float
// cannot be rounded into a unit. Zero is zero.
func DecimalToBaseUnits(value string, decimals int) (*big.Int, error) {
	if decimals < 0 || decimals > 36 {
		return nil, fmt.Errorf("decimals %d out of range", decimals)
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("amount is required")
	}
	if IsNegative(trimmed) {
		return nil, fmt.Errorf("amount %s: %w", trimmed, ErrNegativeAmount)
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid amount %q", trimmed)
	}
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if strings.TrimLeft(whole, "0123456789") != "" || strings.TrimLeft(frac, "0123456789") != "" {
		return nil, fmt.Errorf("invalid amount %q", trimmed)
	}
	if len(frac) > decimals {
		return nil, fmt.Errorf("amount %q has more than %d decimal places", trimmed, decimals)
	}
	frac += strings.Repeat("0", decimals-len(frac))
	combined := strings.TrimLeft(whole+frac, "0")
	if combined == "" {
		return big.NewInt(0), nil
	}
	units, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount %q", trimmed)
	}
	return units, nil
}
