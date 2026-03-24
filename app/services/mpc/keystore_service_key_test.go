package mpc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 64 hex chars = 32 bytes
const testServiceKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestEncryptWithServiceKey_ReturnsValidJSON(t *testing.T) {
	result, err := EncryptWithServiceKey([]byte("my-secret-passphrase"), testServiceKey)
	require.NoError(t, err)

	var parsed map[string]string
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))

	assert.NotEmpty(t, parsed["iv"])
	assert.NotEmpty(t, parsed["ct"])
	assert.Equal(t, "aes-256-gcm", parsed["cipher"])
	// should NOT have salt or kdf fields
	assert.Empty(t, parsed["salt"])
	assert.Empty(t, parsed["kdf"])
}

func TestEncryptWithServiceKey_DifferentNonceEachCall(t *testing.T) {
	data := []byte("passphrase")
	r1, err1 := EncryptWithServiceKey(data, testServiceKey)
	r2, err2 := EncryptWithServiceKey(data, testServiceKey)
	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.NotEqual(t, r1, r2, "each call should use a fresh random nonce")
}

func TestEncryptWithServiceKey_InvalidKeyHex(t *testing.T) {
	_, err := EncryptWithServiceKey([]byte("data"), "not-valid-hex")
	assert.ErrorContains(t, err, "decode key")
}

func TestEncryptWithServiceKey_WrongKeyLength(t *testing.T) {
	// 32 hex chars = 16 bytes, not 32
	_, err := EncryptWithServiceKey([]byte("data"), "0102030405060708090a0b0c0d0e0f10")
	assert.ErrorContains(t, err, "must be 32 bytes")
}
