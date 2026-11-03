package rules

import (
	"regexp"
	"strconv"
	"strings"
)

var cpfNonDigits = regexp.MustCompile(`\D`)

// isValidCPF reports a Brazilian CPF: 11 digits with valid check digits, and
// not one of the all-same-digit sequences the algorithm otherwise accepts. A
// CPF is immutable once a receiver is created upstream, so a strong check here
// is the primary defense against an unfixable wrong-CPF receiver.
func isValidCPF(s string) bool {
	digits := cpfNonDigits.ReplaceAllString(s, "")
	if len(digits) != 11 {
		return false
	}
	if allSameDigit(digits) {
		return false
	}
	return checkDigit(digits, 9) && checkDigit(digits, 10)
}

func allSameDigit(s string) bool {
	return strings.Count(s, string(s[0])) == len(s)
}

// checkDigit validates the verifier digit at position `pos` (0-indexed) using
// the standard CPF weighting (pos+1 down to 2).
func checkDigit(digits string, pos int) bool {
	sum := 0
	weight := pos + 1
	for i := 0; i < pos; i++ {
		d, _ := strconv.Atoi(string(digits[i]))
		sum += d * weight
		weight--
	}
	rem := (sum * 10) % 11
	if rem == 10 {
		rem = 0
	}
	want, _ := strconv.Atoi(string(digits[pos]))
	return rem == want
}
