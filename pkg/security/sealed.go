package security

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Cipher is the encryption facility the application already uses
// (facades.Crypt(): AES-GCM under APP_KEY).
type Cipher interface {
	EncryptString(value string) (string, error)
	DecryptString(payload string) (string, error)
}

var ErrSecretNotSealed = errors.New("stored secret is not sealed")

const (
	gcmNonceSize = 12
	gcmTagSize   = 16
)

// SealSecret encrypts a secret for storage. An empty secret is sealed too,
// so storage never reveals whether a secret was set.
func SealSecret(cipher Cipher, plaintext string) (string, error) {
	if cipher == nil {
		return "", errors.New("seal secret: cipher is required")
	}
	sealed, err := cipher.EncryptString(plaintext)
	if err != nil {
		return "", fmt.Errorf("seal secret: %w", err)
	}
	return sealed, nil
}

// OpenSecret decrypts a stored secret. A value that is not in the sealed
// format is refused rather than used as plaintext, so a row written around
// the sealing path fails loudly.
func OpenSecret(cipher Cipher, stored string) (string, error) {
	if cipher == nil {
		return "", errors.New("open secret: cipher is required")
	}
	if !IsSealedSecret(stored) {
		return "", ErrSecretNotSealed
	}
	plaintext, err := cipher.DecryptString(stored)
	if err != nil {
		return "", fmt.Errorf("open secret: %w", err)
	}
	return plaintext, nil
}

// IsSealedSecret reports whether a stored value has the shape the cipher
// produces: base64 of {"iv": <12-byte nonce>, "value": <ciphertext+tag>}.
// It checks the envelope only; whether the key opens it is OpenSecret's job.
func IsSealedSecret(stored string) bool {
	if stored == "" {
		return false
	}
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
