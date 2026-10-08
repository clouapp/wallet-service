package auth

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"

	"github.com/goravel/framework/contracts/hash"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	appfacades "github.com/macrowallets/waas/app/facades"
)

// Service hashes and checks passwords through the hash facade and handles
// TOTP, recovery codes and tokens.
type Service struct {
	// hasher is the password hasher. Nil means the process hash facade.
	hasher hash.Hash
}

// DummyPasswordHash is a bcrypt hash at cost 10 (the hashing config's rounds) of a random string
// nobody knows. Login compares the submitted password against it when the email
// has no user, so a missing user costs the same bcrypt time as a wrong password.
const DummyPasswordHash = "$2a$10$NJIEW0bsDrp6v6qmmZ4t5.0cbwEo2J8oNSsiEklai1L7uSQVgXIPO"

// NewService hashes passwords with the process hash facade.
func NewService() *Service { return &Service{} }

// NewServiceWithHasher hashes passwords with hasher instead of the facade, for
// callers that run without a booted application.
func NewServiceWithHasher(hasher hash.Hash) *Service { return &Service{hasher: hasher} }

func (s *Service) passwordHasher() hash.Hash {
	if s.hasher != nil {
		return s.hasher
	}
	return appfacades.Hash()
}

func (s *Service) HashPassword(password string) (string, error) {
	return s.passwordHasher().Make(password)
}

func (s *Service) CheckPassword(password, hash string) bool {
	return s.passwordHasher().Check(password, hash)
}

func (s *Service) GenerateTOTP(email string) (secret, qrURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Vault",
		AccountName: email,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

func (s *Service) VerifyTOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}

// GenerateRecoveryCodes returns 10 plaintext codes and their bcrypt hashes.
func (s *Service) GenerateRecoveryCodes() (codes []string, hashes []string, err error) {
	for i := 0; i < 10; i++ {
		b := make([]byte, 10)
		if _, err = rand.Read(b); err != nil {
			return nil, nil, err
		}
		code := base32.StdEncoding.EncodeToString(b)[:16]
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return nil, nil, err
		}
		codes = append(codes, code)
		hashes = append(hashes, string(hash))
	}
	return codes, hashes, nil
}

func (s *Service) VerifyRecoveryCode(code, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(code)) == nil
}

// HashToken produces a bcrypt hash of a raw token (for refresh/reset tokens).
func (s *Service) HashToken(raw string) string {
	hash, _ := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
	return string(hash)
}

func (s *Service) CheckToken(raw, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw)) == nil
}

// GenerateRandomToken returns a cryptographically secure URL-safe token.
func (s *Service) GenerateRandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base32.StdEncoding.EncodeToString(b), nil
}
