package auth_test

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/suite"

	authsvc "github.com/macrowallets/waas/app/services/auth"
)

type AuthServiceTestSuite struct {
	suite.Suite
}

func TestAuthService(t *testing.T) {
	suite.Run(t, new(AuthServiceTestSuite))
}

func (s *AuthServiceTestSuite) TestHashPassword_ReturnsBcryptHash() {
	svc := authsvc.NewService()
	hash, err := svc.HashPassword("mysecret")
	s.NoError(err)
	s.NotEmpty(hash)
	s.True(svc.CheckPassword("mysecret", hash))
}

func (s *AuthServiceTestSuite) TestCheckPassword_WrongPassword_ReturnsFalse() {
	svc := authsvc.NewService()
	hash, _ := svc.HashPassword("correct")
	s.False(svc.CheckPassword("wrong", hash))
}

func (s *AuthServiceTestSuite) TestGenerateTOTP_ReturnsKeyAndQR() {
	svc := authsvc.NewService()
	key, qr, err := svc.GenerateTOTP("user@example.com")
	s.NoError(err)
	s.NotEmpty(key)
	s.NotEmpty(qr)
}

func (s *AuthServiceTestSuite) TestVerifyTOTP_ValidCode_ReturnsTrue() {
	svc := authsvc.NewService()
	key, _, _ := svc.GenerateTOTP("user@example.com")
	code, err := totp.GenerateCode(key, time.Now())
	s.NoError(err)
	s.True(svc.VerifyTOTP(key, code))
}

func (s *AuthServiceTestSuite) TestGenerateRecoveryCodes_Returns10Codes() {
	svc := authsvc.NewService()
	codes, hashes, err := svc.GenerateRecoveryCodes()
	s.NoError(err)
	s.Len(codes, 10)
	s.Len(hashes, 10)
}

func (s *AuthServiceTestSuite) TestVerifyRecoveryCode_MatchesHash() {
	svc := authsvc.NewService()
	codes, hashes, _ := svc.GenerateRecoveryCodes()
	s.True(svc.VerifyRecoveryCode(codes[0], hashes[0]))
	s.False(svc.VerifyRecoveryCode(codes[0], hashes[1]))
}

func (s *AuthServiceTestSuite) TestHashToken_IsDeterministicInCheck() {
	svc := authsvc.NewService()
	raw := "some-refresh-token"
	hash := svc.HashToken(raw)
	s.True(svc.CheckToken(raw, hash))
	s.False(svc.CheckToken("other-token", hash))
}
