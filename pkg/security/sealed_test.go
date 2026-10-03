package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// gcmCipher produces the same envelope as facades.Crypt(): base64 of
// {"iv": nonce, "value": ciphertext+tag}.
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

func TestSealAndOpenSecret_RoundTrip(t *testing.T) {
	c := newGCMCipher(t)
	for _, secret := range []string{"whsec_markets", "", "a secret with spaces and ünïcode", string(make([]byte, 255))} {
		sealed, err := SealSecret(c, secret)
		require.NoError(t, err)
		require.NotEqual(t, secret, sealed)
		require.True(t, IsSealedSecret(sealed))

		opened, err := OpenSecret(c, sealed)
		require.NoError(t, err)
		require.Equal(t, secret, opened)
	}
}

func TestSealSecret_IsRandomised(t *testing.T) {
	c := newGCMCipher(t)
	first, err := SealSecret(c, "same")
	require.NoError(t, err)
	second, err := SealSecret(c, "same")
	require.NoError(t, err)
	require.NotEqual(t, first, second, "a fresh nonce per seal")
}

func TestOpenSecret_RefusesPlaintextAndForeignKeys(t *testing.T) {
	c := newGCMCipher(t)

	_, err := OpenSecret(c, "plaintext-secret")
	require.ErrorIs(t, err, ErrSecretNotSealed)
	_, err = OpenSecret(c, "")
	require.ErrorIs(t, err, ErrSecretNotSealed)

	sealedElsewhere, err := SealSecret(newGCMCipher(t), "secret")
	require.NoError(t, err)
	_, err = OpenSecret(c, sealedElsewhere)
	require.Error(t, err, "a value sealed under another key does not open")
}

func TestIsSealedSecret_RejectsLookalikes(t *testing.T) {
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		return base64.StdEncoding.EncodeToString(raw)
	}
	cases := map[string]string{
		"plaintext":       "whsec_markets",
		"base64 text":     base64.StdEncoding.EncodeToString([]byte("whsec_markets")),
		"json no iv":      encode(map[string][]byte{"value": make([]byte, 32)}),
		"short iv":        encode(map[string][]byte{"iv": make([]byte, 8), "value": make([]byte, 32)}),
		"short value":     encode(map[string][]byte{"iv": make([]byte, 12), "value": make([]byte, 8)}),
		"extra field":     encode(map[string][]byte{"iv": make([]byte, 12), "value": make([]byte, 32), "x": nil}),
		"not an envelope": encode([]string{"iv", "value"}),
	}
	for name, value := range cases {
		require.False(t, IsSealedSecret(value), name)
	}
}

func TestSealAndOpenSecret_ReportCipherFailures(t *testing.T) {
	_, err := SealSecret(failingCipher{}, "secret")
	require.Error(t, err)
	_, err = SealSecret(nil, "secret")
	require.Error(t, err)

	sealed, err := SealSecret(newGCMCipher(t), "secret")
	require.NoError(t, err)
	_, err = OpenSecret(failingCipher{}, sealed)
	require.Error(t, err)
	_, err = OpenSecret(nil, sealed)
	require.Error(t, err)
}
