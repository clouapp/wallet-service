package ingest

import (
	"fmt"

	"github.com/macrowallets/waas/app/services/settings"
)

// SigningSecrets seals and opens the provider signing secrets of webhook_subscriptions:
// written sealed (settings.Seal, enc:v1:), read sealed or as the bare envelope of an
// older row (settings.OpenStored), never as plaintext. The ingest signature check,
// the address sync and ingest:subscribe all go through it.
type SigningSecrets struct {
	cipher settings.Cipher
}

// NewSigningSecrets uses cipher, the application key.
func NewSigningSecrets(cipher settings.Cipher) *SigningSecrets {
	return &SigningSecrets{cipher: cipher}
}

// Seal returns the stored form of a provider signing secret.
func (s *SigningSecrets) Seal(plaintext string) (string, error) {
	if s == nil || s.cipher == nil {
		return "", fmt.Errorf("seal signing secret: cipher is required")
	}
	return settings.Seal(s.cipher, plaintext)
}

// Open returns the signing secret of a stored row; a row that is not sealed fails.
func (s *SigningSecrets) Open(stored string) (string, error) {
	if s == nil || s.cipher == nil {
		return "", fmt.Errorf("open signing secret: cipher is required")
	}
	return settings.OpenStored(s.cipher, stored)
}
