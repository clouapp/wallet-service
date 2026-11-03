// Package pixkeys hashes Pix keys and orchestrates UserPixKeyRepository.
package pixkeys

import (
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/support/brazil"
	"github.com/macrowallets/waas/packages/rampengine/app/support/keyedhash"
)

// HashKey returns a deterministic HMAC-SHA256 hex digest of the normalized key,
// keyed by APP_KEY. It is what identifies a key in user_pix_keys and in
// offramp_destinations.
func HashKey(pixKeyType, pixKey string) string {
	return keyedhash.Hex(brazil.NormalizePixKey(pixKeyType, pixKey))
}

// DestinationDocumentHash digests the holder document a payout destination is
// registered under, or returns "" for a CPF or CNPJ key: both providers
// register that key with itself as the document, whatever was sent alongside
// it. Every other key type carries the document separately, and the same key
// under two documents is two registrations.
//
// The document is digested in its canonical form (brazil.CanonicalDocument),
// the one Ozav's tax_id takes, so "390.533.447-05" and "39053344705" are one
// holder. That form is a numeric document's digits, as it always was, and an
// alphanumeric CNPJ keeps the letters that tell it from another company's.
// The live off-ramp and the offramp_destinations backfill both come through
// here, which is what keeps a backfilled destination findable by a withdrawal
// that writes the document another way.
func DestinationDocumentHash(pixKeyType, recipientDocument string) string {
	switch strings.ToLower(pixKeyType) {
	case "cpf", "cnpj":
		return ""
	}
	return keyedhash.Hex(brazil.CanonicalDocument(recipientDocument))
}
