package settings

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	goravelcrypt "github.com/goravel/framework/contracts/crypt"
	"github.com/goravel/framework/facades"
)

// sealedPrefix tags a value sealed by Seal so Open can refuse a value that
// was stored in the clear.
const sealedPrefix = "enc:v1:"

// Cipher seals and opens stored secrets. Goravel's Crypt facade satisfies it.
type Cipher interface {
	EncryptString(value string) (string, error)
	DecryptString(value string) (string, error)
}

// IsSealed reports whether value carries the sealed marker.
func IsSealed(value string) bool {
	return strings.HasPrefix(value, sealedPrefix)
}

// PrefixSeal tags ciphertext that was encrypted before the marker existed.
// It does not encrypt. An empty value stays empty. A value that already
// carries the marker is returned unchanged. Open still refuses a value that
// never received this marker.
func PrefixSeal(stored string) string {
	if stored == "" || IsSealed(stored) {
		return stored
	}
	return sealedPrefix + stored
}

// ErrNotSealed is an Open of a value that was never sealed. The plaintext
// is not returned.
var ErrNotSealed = errors.New("open setting: value is not sealed")

// Seal encrypts a secret and tags it. Empty plaintext stays empty and is not
// a sealed value. Sealing an already sealed value returns it unchanged.
// The plaintext is never written into the error.
func Seal(c Cipher, plaintext string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("seal setting: cipher is required")
	}
	if plaintext == "" || IsSealed(plaintext) {
		return plaintext, nil
	}
	ciphertext, err := c.EncryptString(plaintext)
	if err != nil {
		return "", fmt.Errorf("seal setting: %w", err)
	}
	return sealedPrefix + ciphertext, nil
}

// Open reverses Seal. A non-empty value without the marker is refused so a
// plaintext row cannot be treated as a secret that was sealed.
func Open(c Cipher, value string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("open setting: cipher is required")
	}
	if value == "" {
		return "", nil
	}
	raw, ok := strings.CutPrefix(value, sealedPrefix)
	if !ok {
		return "", ErrNotSealed
	}
	plaintext, err := c.DecryptString(raw)
	if err != nil {
		return "", fmt.Errorf("open setting: %w", err)
	}
	return plaintext, nil
}

// Rows sealed before the prefix existed hold the bare Crypt envelope: base64
// of {"iv": <12-byte nonce>, "value": <ciphertext+tag>}. They are still read,
// never written.
const (
	gcmNonceSize = 12
	gcmTagSize   = 16
)

// isLegacyEnvelope reports whether stored has the shape Crypt produces. It
// checks the envelope only; whether the key opens it is the cipher's job.
func isLegacyEnvelope(stored string) bool {
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return false
	}
	var envelope map[string][]byte
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope) != 2 {
		return false
	}
	iv, hasIV := envelope["iv"]
	value, hasValue := envelope["value"]
	return hasIV && hasValue && len(iv) == gcmNonceSize && len(value) >= gcmTagSize
}

// OpenStored opens a secret in either stored format: the enc:v1: prefix Seal
// writes, or the bare Crypt envelope older rows hold. Unlike Open, an empty
// value is refused, because it is for secrets that must have content (a chain
// RPC endpoint). A value in neither format is refused rather than used as
// plaintext. The plaintext is never written into the error.
func OpenStored(c Cipher, stored string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("open setting: cipher is required")
	}
	if stored == "" {
		return "", ErrNotSealed
	}
	if IsSealed(stored) {
		return Open(c, stored)
	}
	if !isLegacyEnvelope(stored) {
		return "", ErrNotSealed
	}
	plaintext, err := c.DecryptString(stored)
	if err != nil {
		return "", fmt.Errorf("open setting: %w", err)
	}
	return plaintext, nil
}

// CryptSealer seals and opens with the process Crypt facade.
type CryptSealer struct{}

func processCipher() (Cipher, error) {
	cipher := facades.Crypt()
	if cipher == nil {
		return nil, fmt.Errorf("seal setting: crypt is not available")
	}
	return cipherAdapter{cipher}, nil
}

// Seal encrypts plaintext for storage.
func (CryptSealer) Seal(plaintext string) (string, error) {
	cipher, err := processCipher()
	if err != nil {
		return "", err
	}
	return Seal(cipher, plaintext)
}

// Open reverses a value sealed by CryptSealer. The plaintext is not logged.
func (CryptSealer) Open(value string) (string, error) {
	cipher, err := processCipher()
	if err != nil {
		return "", err
	}
	return Open(cipher, value)
}

// cipherAdapter keeps Seal on the methods it needs. Goravel's Crypt is wider.
type cipherAdapter struct {
	crypt goravelcrypt.Crypt
}

func (c cipherAdapter) EncryptString(value string) (string, error) {
	return c.crypt.EncryptString(value)
}

func (c cipherAdapter) DecryptString(value string) (string, error) {
	return c.crypt.DecryptString(value)
}
