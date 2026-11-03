package rules

import (
	"context"
	"regexp"
	"strings"

	"github.com/goravel/framework/contracts/validation"
)

var (
	// cpfDocumentPattern is a CPF as one word: eleven digits.
	cpfDocumentPattern = regexp.MustCompile(`^[0-9]{11}$`)
	// cnpjDocumentShape is a CNPJ as one word, in either case: twelve letters
	// or digits, then two check digits.
	cnpjDocumentShape = regexp.MustCompile(`^[0-9A-Za-z]{12}[0-9]{2}$`)
)

// RecipientDocumentRule accepts the holder document beside an email, phone
// or random Pix key as one word naming a real holder: a CPF or a CNPJ —
// numeric, or alphanumeric since 31/07/2026 (spec §19.1) — with its check
// digits right. An installed iOS build strips the letters of an alphanumeric
// CNPJ and sends what is left, a number that names nobody, which reached the
// provider as the holder; a check digit refuses it here instead.
type RecipientDocumentRule struct{}

func (r *RecipientDocumentRule) Signature() string { return "recipient_document" }

func (r *RecipientDocumentRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	value, ok := val.(string)
	switch {
	case !ok:
		return false
	case cpfDocumentPattern.MatchString(value):
		return isValidCPF(value)
	case cnpjDocumentShape.MatchString(value):
		return isValidCNPJ(strings.ToUpper(value))
	}
	return false
}

func (r *RecipientDocumentRule) Message(_ context.Context) string {
	return "must be a CPF or CNPJ, without punctuation"
}
