package ingest

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/settings"
)

// reversingCipher stands in for the application key: what it encrypts does not read
// as the plaintext, and only it opens what it encrypted.
type reversingCipher struct{}

func (reversingCipher) EncryptString(value string) (string, error) {
	runes := []rune(value)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return base64.StdEncoding.EncodeToString([]byte("rev:" + string(runes))), nil
}

func (reversingCipher) DecryptString(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || !strings.HasPrefix(string(raw), "rev:") {
		return "", errors.New("not encrypted with this key")
	}
	runes := []rune(strings.TrimPrefix(string(raw), "rev:"))
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes), nil
}

const providerSecret = "whsec-provider-7f3a"

func TestSigning_Secrets_SealWritesTheSealedFormat(t *testing.T) {
	stored, err := NewSigningSecrets(reversingCipher{}).Seal(providerSecret)
	require.NoError(t, err)

	assert.True(t, settings.IsSealed(stored), "stored as every secret is (enc:v1:)")
	assert.NotContains(t, stored, providerSecret)
}

func TestSigning_Secrets_OpenReadsWhatSealWrote(t *testing.T) {
	secrets := NewSigningSecrets(reversingCipher{})
	stored, err := secrets.Seal(providerSecret)
	require.NoError(t, err)

	opened, err := secrets.Open(stored)
	require.NoError(t, err)
	assert.Equal(t, providerSecret, opened)
}

func TestSigning_Secrets_OpenRefusesAPlaintextRow(t *testing.T) {
	_, err := NewSigningSecrets(reversingCipher{}).Open(providerSecret)
	assert.Error(t, err, "a row stored unsealed is not a secret anyone may verify with")
}

func TestSigning_Secrets_RefuseWithoutACipher(t *testing.T) {
	_, err := NewSigningSecrets(nil).Seal(providerSecret)
	assert.Error(t, err)
	_, err = NewSigningSecrets(nil).Open("enc:v1:x")
	assert.Error(t, err)
}
