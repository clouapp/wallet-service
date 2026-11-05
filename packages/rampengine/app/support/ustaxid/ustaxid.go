// Package ustaxid reads the United States' individual tax ids, the Social
// Security Number and the Individual Taxpayer Identification Number, the way
// every layer holds them: nine digits, with the dashes and spaces a person
// types dropped.
package ustaxid

import (
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
)

// Digits is the nine digits of id, dashes and spaces dropped. ok is false when
// id holds any other character or not exactly nine digits.
func Digits(id string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(id) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == ' ':
		default:
			return "", false
		}
	}
	if b.Len() != 9 {
		return "", false
	}
	return b.String(), true
}

// ValidSSN reports whether id is a Social Security Number the SSA could have
// issued: no area 000, 666 or 9xx, no group 00, no serial 0000. One repeated
// digit and 123456789 are refused too: Bridge flags them as
// tax_identification_number_fake_pattern (sandbox, 14/10/2026).
func ValidSSN(id string) bool {
	d, ok := Digits(id)
	if !ok {
		return false
	}
	area, group, serial := d[0:3], d[3:5], d[5:9]
	if area == "000" || area == "666" || area[0] == '9' || group == "00" || serial == "0000" {
		return false
	}
	return d != "123456789" && strings.Count(d, d[:1]) != len(d)
}

// ValidITIN reports whether id is an ITIN as the IRS issues them: a leading 9
// and a group (the fourth and fifth digits) in 50-65, 70-88, 90-92 or 94-99.
// Serial 0000 and one repeated digit are refused, as for an SSN.
func ValidITIN(id string) bool {
	d, ok := Digits(id)
	if !ok || d[0] != '9' || d[5:9] == "0000" || strings.Count(d, d[:1]) == len(d) {
		return false
	}
	group := int(d[3]-'0')*10 + int(d[4]-'0')
	switch {
	case group >= 50 && group <= 65, group >= 70 && group <= 88,
		group >= 90 && group <= 92, group >= 94 && group <= 99:
		return true
	}
	return false
}

// Canonical is id by its nine digits, or id trimmed when it is not nine
// digits, for validation to refuse.
func Canonical(id string) string {
	if d, ok := Digits(id); ok {
		return d
	}
	return strings.TrimSpace(id)
}

// Last4 is the last four digits of id, "" when id is not nine digits.
func Last4(id string) string {
	d, ok := Digits(id)
	if !ok {
		return ""
	}
	return digits.Last4(d)
}
