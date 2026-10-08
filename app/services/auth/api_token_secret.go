package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// apiTokenSecretBytes is the random secret a new API token carries in its JWT.
const apiTokenSecretBytes = 32

// GenerateAPITokenSecret returns a random 32-byte secret, hex-encoded, for the
// JWT secret claim. The caller shows that JWT once and stores only the sha256.
func (s *Service) GenerateAPITokenSecret() (string, error) {
	if s == nil {
		return "", fmt.Errorf("api token secret: auth service is required")
	}
	raw := make([]byte, apiTokenSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("api token secret: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// HashAPITokenSecret is the token_hash of a new API token: hex sha256 of the
// secret claim. The secret itself is not stored.
func (s *Service) HashAPITokenSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// IsAPITokenSecretHash reports whether token_hash is a sha256 digest. The
// previous form (a bcrypt hash of the token id, or any other value already
// in the database) is not a 32-byte hex digest.
func IsAPITokenSecretHash(stored string) bool {
	if len(stored) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(stored)
	return err == nil && len(decoded) == sha256.Size
}

// APITokenSecretMatches reports whether sha256(secret) equals the stored digest.
// The comparison is over the raw digest bytes.
func APITokenSecretMatches(secret, stored string) bool {
	if secret == "" || !IsAPITokenSecretHash(stored) {
		return false
	}
	sum := sha256.Sum256([]byte(secret))
	decoded, err := hex.DecodeString(stored)
	if err != nil || len(decoded) != len(sum) {
		return false
	}
	return hmac.Equal(sum[:], decoded)
}

// APITokenHashAccepts is the verify rule. A sha256 digest matches only the
// secret claim. Any other value already stored (bcrypt of the token id, or a
// hash written before the secret claim existed) still authenticates when the
// JWT carries no secret claim.
func APITokenHashAccepts(presentedSecret, stored string) bool {
	if stored == "" {
		return false
	}
	if IsAPITokenSecretHash(stored) {
		return APITokenSecretMatches(presentedSecret, stored)
	}
	return presentedSecret == ""
}
