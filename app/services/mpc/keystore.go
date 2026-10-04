package mpc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/macrowallets/waas/pkg/mpcshare"
)

// Customer-share envelope. The type and the Argon2id/AES-GCM functions live in
// pkg/mpcshare so models can decrypt share A without importing this service.
// These names stay so callers and errors.Is keep the same sentinel and format.
type EncryptedShare = mpcshare.EncryptedShare

// ErrInvalidPassphrase is the same sentinel pkg/mpcshare returns on AES-GCM failure.
var ErrInvalidPassphrase = mpcshare.ErrInvalidPassphrase

// EncryptShare derives a key from passphrase via Argon2id then encrypts share
// with AES-256-GCM. Returns ciphertext with the GCM tag appended.
func EncryptShare(share []byte, passphrase string) (*EncryptedShare, error) {
	return mpcshare.EncryptShare(share, passphrase)
}

// DecryptShare reverses EncryptShare. Returns ErrInvalidPassphrase on auth failure.
func DecryptShare(enc *EncryptedShare, passphrase string) ([]byte, error) {
	return mpcshare.DecryptShare(enc, passphrase)
}

// EncryptWithServiceKey encrypts data with a service-held AES-256-GCM key.
// keyHex must be a 64-character hex string (32 bytes, high-entropy machine key).
// No KDF is applied. Returns JSON: {"iv":"<b64>","ct":"<b64>","cipher":"aes-256-gcm"}.
func EncryptWithServiceKey(data []byte, keyHex string) (string, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return "", fmt.Errorf("decode key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("service key must be 32 bytes, got %d", len(key))
	}

	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	ct := gcm.Seal(nil, nonce, data, nil)

	type payload struct {
		IV     string `json:"iv"`
		CT     string `json:"ct"`
		Cipher string `json:"cipher"`
	}
	p := payload{
		IV:     base64.StdEncoding.EncodeToString(nonce),
		CT:     base64.StdEncoding.EncodeToString(ct),
		Cipher: "aes-256-gcm",
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
