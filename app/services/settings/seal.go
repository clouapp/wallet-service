package settings

import (
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

// Seal encrypts a secret and tags it. Empty plaintext stays empty and is not
// a sealed value. The plaintext is never written into the error.
func Seal(c Cipher, plaintext string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("seal setting: cipher is required")
	}
	if plaintext == "" {
		return "", nil
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
		return "", fmt.Errorf("open setting: value is not sealed")
	}
	plaintext, err := c.DecryptString(raw)
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
