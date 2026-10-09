package migrations

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Private copies of the sealing helpers the 440-590 migrations ran against when
// they were written (app/services/settings seal.go, pkg/security sealed.go).
// A migration that already ran must keep doing the same on a fresh database
// whatever those packages become, so nothing here follows them: do not edit
// this file to match a change there.

// mfaSubjectUsers is the mfa_credentials.subject_type of a dashboard user.
const mfaSubjectUsers = "users"

// sealedPrefix tags a value sealed by sealSetting so openSetting can refuse a
// value that was stored in the clear.
const sealedPrefix = "enc:v1:"

const (
	gcmNonceSize = 12
	gcmTagSize   = 16
)

// sealCipher seals and opens stored secrets. Goravel's Crypt facade satisfies it.
type sealCipher interface {
	EncryptString(value string) (string, error)
	DecryptString(value string) (string, error)
}

var errSecretNotSealed = errors.New("stored secret is not sealed")

func isSealedSetting(value string) bool {
	return strings.HasPrefix(value, sealedPrefix)
}

// prefixSeal tags ciphertext that was encrypted before the marker existed. It
// does not encrypt.
func prefixSeal(stored string) string {
	if stored == "" || isSealedSetting(stored) {
		return stored
	}
	return sealedPrefix + stored
}

// sealSetting encrypts a secret and tags it. Empty plaintext stays empty and an
// already sealed value is returned unchanged.
func sealSetting(c sealCipher, plaintext string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("seal setting: cipher is required")
	}
	if plaintext == "" || isSealedSetting(plaintext) {
		return plaintext, nil
	}
	ciphertext, err := c.EncryptString(plaintext)
	if err != nil {
		return "", fmt.Errorf("seal setting: %w", err)
	}
	return sealedPrefix + ciphertext, nil
}

// openSetting reverses sealSetting. A non-empty value without the marker is refused.
func openSetting(c sealCipher, value string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("open setting: cipher is required")
	}
	if value == "" {
		return "", nil
	}
	raw, ok := strings.CutPrefix(value, sealedPrefix)
	if !ok {
		return "", errors.New("open setting: value is not sealed")
	}
	plaintext, err := c.DecryptString(raw)
	if err != nil {
		return "", fmt.Errorf("open setting: %w", err)
	}
	return plaintext, nil
}

// sealSecret encrypts a secret for storage. An empty secret is sealed too.
func sealSecret(cipher sealCipher, plaintext string) (string, error) {
	if cipher == nil {
		return "", errors.New("seal secret: cipher is required")
	}
	sealed, err := cipher.EncryptString(plaintext)
	if err != nil {
		return "", fmt.Errorf("seal secret: %w", err)
	}
	return sealed, nil
}

// openSecret decrypts a stored secret and refuses a value that is not in the
// envelope format.
func openSecret(cipher sealCipher, stored string) (string, error) {
	if cipher == nil {
		return "", errors.New("open secret: cipher is required")
	}
	if !isSealedSecret(stored) {
		return "", errSecretNotSealed
	}
	plaintext, err := cipher.DecryptString(stored)
	if err != nil {
		return "", fmt.Errorf("open secret: %w", err)
	}
	return plaintext, nil
}

// isSealedSecret reports whether a stored value has the shape the cipher
// produces: base64 of {"iv": <12-byte nonce>, "value": <ciphertext+tag>}.
func isSealedSecret(stored string) bool {
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
