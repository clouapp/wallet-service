package rules

import (
	"regexp"

	"github.com/macrowallets/waas/packages/rampengine/app/support/brazil"
)

// cnpjTypedPattern is a CNPJ as people write one: digits, the letters an
// alphanumeric CNPJ carries in either case, and the dots, dashes, slashes,
// parentheses, spaces and plus sign a CPF, CNPJ or phone is typed with.
var cnpjTypedPattern = regexp.MustCompile(`^[\dA-Za-z\s.\-/()+]+$`)

// cnpjWeights are the weights of the first and second CNPJ check digits over
// the characters before each.
var (
	cnpjWeights1 = []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	cnpjWeights2 = []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
)

// isValidCNPJ reports a Brazilian CNPJ given in its canonical spelling
// (brazil.CanonicalDocument): fourteen characters, the first twelve digits or
// upper-case letters and the last two digits, both check digits right, and
// not one of the all-same sequences the algorithm otherwise accepts.
//
// Letters entered the first twelve positions on 31/07/2026 and numeric CNPJs
// stay valid. The check digit is the same modulo 11 with the same weights,
// each character worth its ASCII code minus 48: a digit is itself, A is 17
// and Z is 42 (Receita's alphanumeric CNPJ rule; spec §16).
func isValidCNPJ(cnpj string) bool {
	if len(cnpj) != 14 || allSameDigit(cnpj) {
		return false
	}
	values := make([]int, len(cnpj))
	for i, r := range cnpj {
		digit := r >= '0' && r <= '9'
		if !digit && (i >= 12 || r < 'A' || r > 'Z') {
			return false
		}
		values[i] = int(r - '0')
	}
	return values[12] == cnpjCheckDigit(values, cnpjWeights1) && values[13] == cnpjCheckDigit(values, cnpjWeights2)
}

func cnpjCheckDigit(values, weights []int) int {
	sum := 0
	for i, w := range weights {
		sum += values[i] * w
	}
	if rem := sum % 11; rem >= 2 {
		return 11 - rem
	}
	return 0
}

// isValidTypedCNPJ reports a CNPJ however it was typed: masked or bare, its
// letters in either case.
func isValidTypedCNPJ(typed string) bool {
	return cnpjTypedPattern.MatchString(typed) && isValidCNPJ(brazil.CanonicalDocument(typed))
}
