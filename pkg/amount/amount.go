// Package amount converts between integer base units (wei, satoshi, lamport, token
// base units) and human decimal strings with exact integer arithmetic.
package amount

import (
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
