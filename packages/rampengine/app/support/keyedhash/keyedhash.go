// Package keyedhash digests a personal identifier into a value a unique index
// or a lookup can compare without the identifier itself being stored.
//
// It is keyed rather than a plain SHA-256 because the inputs are small spaces:
// every CPF, every Brazilian phone, every Pix key can be enumerated, so an
// unkeyed digest is the identifier in all but name. Keyed by APP_KEY, the same
// key facades.Crypt() encrypts the identifier itself with, so a digest is no
// easier to reverse than the ciphertext next to it.
package keyedhash

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// Hex returns the HMAC-SHA256 hex digest of value, keyed by APP_KEY. The
// caller normalizes value first: two spellings of one identifier are two
// digests here.
func Hex(value string) string {
	mac := hmac.New(sha256.New, []byte(facades.Config().GetString("app.key")))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
