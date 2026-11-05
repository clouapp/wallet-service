package pixkeys

import (
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/support/brazil"
	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
)

// Mask redacts a Pix key for display. The full key is PII and never leaves the
// server unmasked; the client only needs enough to recognize which account was
// paid.
func Mask(key, keyType string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(keyType)) {
	case "cpf":
		digits := digits.Only(key)
		if len(digits) == 11 {
			return "***." + digits[3:6] + "." + digits[6:9] + "-**"
		}
		return strings.Repeat("*", len(key))
	case "cnpj":
		cnpj := brazil.CanonicalDocument(key)
		if len(cnpj) == 14 {
			return cnpj[0:2] + ".***.***/****-" + cnpj[12:14]
		}
		return strings.Repeat("*", len(key))
	case "email":
		at := strings.LastIndex(key, "@")
		if at > 1 {
			local := key[:at]
			domain := key[at+1:]
			return maskMiddle(local, 1, 1) + "@" + domain
		}
	case "phone":
		digits := digits.Only(key)
		if len(digits) >= 4 {
			return "****" + digits[len(digits)-4:]
		}
	}
	return strings.Repeat("*", len(key))
}

func maskMiddle(value string, keepPrefix, keepSuffix int) string {
	if keepPrefix+keepSuffix >= len(value) {
		return strings.Repeat("*", len(value))
	}
	prefix := value[:keepPrefix]
	suffix := value[len(value)-keepSuffix:]
	hidden := len(value) - keepPrefix - keepSuffix
	return prefix + strings.Repeat("*", hidden) + suffix
}
