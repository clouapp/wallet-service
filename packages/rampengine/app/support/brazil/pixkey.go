package brazil

import (
	"regexp"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
)

// NormalizePixKey spells a Pix key the one way the DICT stores it: a CPF as
// digits, a CNPJ as its canonical characters (CanonicalDocument), a phone as
// +55 with DDD and number, an email or random key trimmed and lowercased. Two
// spellings of one key normalize alike, which is what lets a key be
// recognized however it was typed. A phone that is no Brazilian number has no
// such form and comes back as its digits.
func NormalizePixKey(pixKeyType, pixKey string) string {
	switch strings.ToLower(pixKeyType) {
	case "cpf":
		return digits.Only(pixKey)
	case "cnpj":
		return CanonicalDocument(pixKey)
	case "phone":
		return normalizePhonePixKey(pixKey)
	default:
		return strings.ToLower(strings.TrimSpace(pixKey))
	}
}

// CanonicalDocument spells a CPF or CNPJ as the characters that identify it:
// its digits and, for an alphanumeric CNPJ (issued since 31/07/2026), its
// letters in upper case, without punctuation. 12.abc.345/01de-35 is
// 12ABC34501DE35.
//
// On a document of digits and punctuation it answers exactly what digits.Only
// does, which is why letting letters in changed no stored hash: a numeric CNPJ
// keeps the canonical form it always had (spec §19.1).
func CanonicalDocument(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		}
	}
	return b.String()
}

var phoneNationalRegex = regexp.MustCompile(`^([1-9]\d)(9\d{8}|\d{8})$`)

func normalizePhonePixKey(pixKey string) string {
	trimmed := strings.TrimSpace(pixKey)
	digits := digits.Only(trimmed)

	var national string
	switch {
	case strings.HasPrefix(trimmed, "+55"):
		national = strings.TrimPrefix(digits, "55")
	case strings.HasPrefix(digits, "55") && len(digits) >= 12 && len(digits) <= 13:
		national = strings.TrimPrefix(digits, "55")
	case len(digits) >= 10 && len(digits) <= 11:
		national = digits
	default:
		return digits
	}

	if !phoneNationalRegex.MatchString(national) {
		return digits
	}
	return "+55" + national
}
