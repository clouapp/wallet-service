package models

import (
	"strings"
	"unicode"

	"github.com/macrowallets/waas/packages/rampengine/app/support/brazil"
)

// CanonicalBusinessTaxID spells a company's tax id of taxIDType the one way it
// is stored and digested: a CNPJ as brazil.CanonicalDocument spells it (its
// Pix key's form, which the company's off-ramp is compared by), any other
// type upper-cased with only its letters, digits and an RFC's ampersand —
// the spaces, dots, dashes and slashes its forms accept dropped.
func CanonicalBusinessTaxID(taxIDType, taxID string) string {
	if taxIDType == TaxIDTypeCNPJ {
		return brazil.CanonicalDocument(taxID)
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(taxID) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '&' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
