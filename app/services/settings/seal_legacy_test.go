package settings

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gcmCipher produces the same envelope as facades.Crypt(): base64 of
// {"iv": nonce, "value": ciphertext+tag}. Rows sealed before the enc:v1:
// prefix hold exactly that.
type gcmCipher struct{ aead cipher.AEAD }

func newGCMCipher(t *testing.T) gcmCipher {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)
	return gcmCipher{aead: aead}
}

func (c gcmCipher) EncryptString(value string) (string, error) {
	iv := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	envelope, err := json.Marshal(map[string][]byte{"iv": iv, "value": c.aead.Seal(nil, iv, []byte(value), nil)})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(envelope), nil
}

func (c gcmCipher) DecryptString(payload string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", err
	}
	var envelope map[string][]byte
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", err
	}
	plaintext, err := c.aead.Open(nil, envelope["iv"], envelope["value"], nil)
	return string(plaintext), err
}

type failingCipher struct{}

func (failingCipher) EncryptString(string) (string, error) { return "", errors.New("no key") }
func (failingCipher) DecryptString(string) (string, error) { return "", errors.New("no key") }

func TestOpenStored_OpensBothStoredFormats(t *testing.T) {
	c := newGCMCipher(t)
	const secret = "https://rpc.example/key"

	legacy, err := c.EncryptString(secret)
	require.NoError(t, err)
	assert.False(t, IsSealed(legacy), "the bare envelope has no prefix")
	opened, err := OpenStored(c, legacy)
	require.NoError(t, err)
	assert.Equal(t, secret, opened, "a row written before the prefix still opens")

	prefixed, err := Seal(c, secret)
	require.NoError(t, err)
	opened, err = OpenStored(c, prefixed)
	require.NoError(t, err)
	assert.Equal(t, secret, opened)
}

func TestSeal_WritesThePrefixedFormatOverARealCipher(t *testing.T) {
	c := newGCMCipher(t)
	sealed, err := Seal(c, "secret")
	require.NoError(t, err)
	assert.True(t, IsSealed(sealed))
	assert.False(t, isLegacyEnvelope(sealed), "new writes are never the bare envelope")
}

func TestOpenStored_RefusesWhatWasNeverSealed(t *testing.T) {
	c := newGCMCipher(t)
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		return base64.StdEncoding.EncodeToString(raw)
	}
	cases := map[string]string{
		"empty":           "",
		"plaintext":       "whsec_markets",
		"base64 text":     base64.StdEncoding.EncodeToString([]byte("whsec_markets")),
		"json no iv":      encode(map[string][]byte{"value": make([]byte, 32)}),
		"short iv":        encode(map[string][]byte{"iv": make([]byte, 8), "value": make([]byte, 32)}),
		"short value":     encode(map[string][]byte{"iv": make([]byte, 12), "value": make([]byte, 8)}),
		"extra field":     encode(map[string][]byte{"iv": make([]byte, 12), "value": make([]byte, 32), "x": nil}),
		"not an envelope": encode([]string{"iv", "value"}),
	}
	for name, value := range cases {
		_, err := OpenStored(c, value)
		assert.ErrorIs(t, err, ErrNotSealed, name)
	}
}

func TestOpenStored_ReportsCipherFailures(t *testing.T) {
	legacy, err := newGCMCipher(t).EncryptString("secret")
	require.NoError(t, err)

	_, err = OpenStored(nil, legacy)
	assert.Error(t, err)
	_, err = OpenStored(failingCipher{}, legacy)
	assert.Error(t, err)
	_, err = OpenStored(newGCMCipher(t), legacy)
	assert.Error(t, err, "a value sealed under another key does not open")
}

// The strict Open keeps refusing the bare envelope: settings, webhook secrets
// and TOTP rows were all backfilled to the prefix, so a bare one is a row
// written around the sealing path.
func TestOpen_StillRefusesTheBareEnvelope(t *testing.T) {
	c := newGCMCipher(t)
	legacy, err := c.EncryptString("secret")
	require.NoError(t, err)
	_, err = Open(c, legacy)
	assert.ErrorIs(t, err, ErrNotSealed)
}

// An empty plaintext is stored empty by Seal and read back empty by Open; a
// stored secret that must have content (an RPC endpoint) goes through
// OpenStored, which refuses the empty value.
func TestEmptyValues_KeepEachReadersMeaning(t *testing.T) {
	c := newGCMCipher(t)
	sealed, err := Seal(c, "")
	require.NoError(t, err)
	assert.Equal(t, "", sealed)
	opened, err := Open(c, "")
	require.NoError(t, err)
	assert.Equal(t, "", opened)
	_, err = OpenStored(c, "")
	assert.ErrorIs(t, err, ErrNotSealed)
}
