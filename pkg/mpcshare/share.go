// Package mpcshare is the customer-share envelope. Models and the MPC service
// both use it: models may not import app/services, and both may import pkg.
// Argon2id and AES-GCM parameters are the persisted ciphertext format.
package mpcshare

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// EncryptedShare holds all data needed to decrypt a key share later.
type EncryptedShare struct {
	Ciphertext []byte // AES-256-GCM ciphertext || 16-byte GCM tag
	IV         []byte // 12-byte nonce
	Salt       []byte // 16-byte Argon2id salt
}

// EncryptShare derives a key from passphrase via Argon2id then encrypts share
// with AES-256-GCM. Returns ciphertext with the GCM tag appended.
func EncryptShare(share []byte, passphrase string) (*EncryptedShare, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	iv := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("generate iv: %w", err)
	}

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}

	// Seal appends the GCM tag to the ciphertext
	ciphertext := gcm.Seal(nil, iv, share, nil)

	// Zero-wipe the derived key
	for i := range key {
		key[i] = 0
	}

	return &EncryptedShare{
		Ciphertext: ciphertext,
		IV:         iv,
		Salt:       salt,
	}, nil
}

// ErrInvalidPassphrase is returned when AES-GCM authentication fails.
var ErrInvalidPassphrase = errors.New("invalid passphrase")

// gcmNonceSize is the only nonce length cipher.NewGCM accepts; Open panics on others.
const gcmNonceSize = 12

// DecryptShare reverses EncryptShare. Returns ErrInvalidPassphrase on auth failure.
func DecryptShare(enc *EncryptedShare, passphrase string) ([]byte, error) {
	if enc == nil {
		return nil, fmt.Errorf("encrypted share is required")
	}
	if len(enc.IV) != gcmNonceSize {
		return nil, fmt.Errorf("encrypted share iv must be %d bytes, got %d", gcmNonceSize, len(enc.IV))
	}
	key := deriveKey(passphrase, enc.Salt)
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}

	plaintext, err := gcm.Open(nil, enc.IV, enc.Ciphertext, nil)
	if err != nil {
		return nil, ErrInvalidPassphrase
	}

	return plaintext, nil
}

// deriveKey runs Argon2id with the spec parameters.
func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 4, 32)
}
