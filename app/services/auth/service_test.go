package auth_test

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/suite"
	"golang.org/x/crypto/bcrypt"

	authsvc "github.com/macrowallets/waas/app/services/auth"
)

type AuthServiceTestSuite struct {
	suite.Suite
}

func TestService_Auth_Service(t *testing.T) {
	suite.Run(t, new(AuthServiceTestSuite))
}

func (s *AuthServiceTestSuite) TestHash_Password_ReturnsBcryptHash() {
	svc := authsvc.NewService()
	hash, err := svc.HashPassword("mysecret")
	s.Require().NoError(err)
	s.Require().NotEmpty(hash)
	s.True(svc.CheckPassword("mysecret", hash))
}

func (s *AuthServiceTestSuite) TestCheckPassword_WrongPassword_ReturnsFalse() {
	svc := authsvc.NewService()
	hash, _ := svc.HashPassword("correct")
	s.False(svc.CheckPassword("wrong", hash))
}

func (s *AuthServiceTestSuite) TestGenerate_TOTP_ReturnsKeyAndQR() {
	svc := authsvc.NewService()
	key, qr, err := svc.GenerateTOTP("user@example.com")
	s.Require().NoError(err)
	s.NotEmpty(key)
	s.NotEmpty(qr)
}

func (s *AuthServiceTestSuite) TestVerifyTOTP_ValidCode_ReturnsTrue() {
	svc := authsvc.NewService()
	key, _, _ := svc.GenerateTOTP("user@example.com")
	code, err := totp.GenerateCode(key, time.Now())
	s.Require().NoError(err)
	s.True(svc.VerifyTOTP(key, code))
}

func (s *AuthServiceTestSuite) TestGenerate_RecoveryCodes_Returns10Codes() {
	svc := authsvc.NewService()
	codes, hashes, err := svc.GenerateRecoveryCodes()
	s.Require().NoError(err)
	s.Len(codes, 10)
	s.Len(hashes, 10)
}

func (s *AuthServiceTestSuite) TestVerify_RecoveryCode_MatchesHash() {
	svc := authsvc.NewService()
	codes, hashes, _ := svc.GenerateRecoveryCodes()
	s.True(svc.VerifyRecoveryCode(codes[0], hashes[0]))
	s.False(svc.VerifyRecoveryCode(codes[0], hashes[1]))
}

func (s *AuthServiceTestSuite) TestHash_Token_IsDeterministicInCheck() {
	svc := authsvc.NewService()
	raw := "some-refresh-token"
	hash := svc.HashToken(raw)
	s.True(svc.CheckToken(raw, hash))
	s.False(svc.CheckToken("other-token", hash))
}

func TestDummy_PasswordHash_CostsTheSameAsARealOne(t *testing.T) {
	cost, err := bcrypt.Cost([]byte(authsvc.DummyPasswordHash))
	if err != nil {
		t.Fatalf("DummyPasswordHash is not a bcrypt hash: %v", err)
	}
	if cost != bcrypt.DefaultCost {
		t.Fatalf("cost = %d, HashPassword uses %d", cost, bcrypt.DefaultCost)
	}
	if authsvc.NewService().CheckPassword("", authsvc.DummyPasswordHash) {
		t.Fatal("the dummy hash must not match an empty password")
	}
}
