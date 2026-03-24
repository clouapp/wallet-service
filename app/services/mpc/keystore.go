package mpc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

// DecryptShare reverses EncryptShare. Returns ErrInvalidPassphrase on auth failure.
func DecryptShare(enc *EncryptedShare, passphrase string) ([]byte, error) {
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

// deriveKey runs Argon2id with the spec parameters.
func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 4, 32)
}
