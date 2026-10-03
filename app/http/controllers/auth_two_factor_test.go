package controllers_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/tests/mocks"
)

// TwoFactorLoginTestSuite drives the password + TOTP login over HTTP against
// the real routes, middleware, Postgres and the test Redis index.
type TwoFactorLoginTestSuite struct {
	authSuite
}

func TestTwoFactorLoginSuite(t *testing.T) {
	suite.Run(t, new(TwoFactorLoginTestSuite))
}

func (s *TwoFactorLoginTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *TwoFactorLoginTestSuite) TestLoginWithTOTPReturnsAChallengeAndNoSession() {
	user := s.seedUser(true)

	resp, body := s.loginAs(user.Email)

	resp.AssertStatus(200)
	s.True(body.Requires2FA)
	s.NotEmpty(body.PartialToken)
	s.Positive(body.ExpiresIn)
	s.Empty(body.AccessToken, "the password alone must not earn a session")
	s.Empty(body.RefreshToken)
}

func (s *TwoFactorLoginTestSuite) TestSessionAuthRejectsThePartialToken() {
	user := s.seedUser(true)
	_, body := s.loginAs(user.Email)
	s.Require().NotEmpty(body.PartialToken)

	s.getMe(body.PartialToken).AssertStatus(401)

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+body.PartialToken).
		WithHeader("X-Account-Id", user.ID.String()).
		Get("/v1/wallets")
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestValidTOTPCompletesTheLogin() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)

	resp, body := s.verifyTwoFactor(challenge.PartialToken, s.currentCode(user.TOTPSecret), "")

	resp.AssertStatus(200)
	s.NotEmpty(body.AccessToken)
	s.NotEmpty(body.RefreshToken)
	s.getMe(body.AccessToken).AssertOk()
}

func (s *TwoFactorLoginTestSuite) TestPartialTokenIsSingleUse() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)
	resp, _ := s.verifyTwoFactor(challenge.PartialToken, s.currentCode(user.TOTPSecret), "")
	resp.AssertStatus(200)

	resp, _ = s.verifyTwoFactor(challenge.PartialToken, "", user.RecoveryCodes[0])

	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestReplayedCodeIsRefused() {
	user := s.seedUser(true)
	code := s.currentCode(user.TOTPSecret)
	_, first := s.loginAs(user.Email)
	resp, _ := s.verifyTwoFactor(first.PartialToken, code, "")
	resp.AssertStatus(200)

	_, second := s.loginAs(user.Email)
	resp, _ = s.verifyTwoFactor(second.PartialToken, code, "")

	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestRecoveryCodeStillWorksAndIsSingleUse() {
	user := s.seedUser(true)

	_, first := s.loginAs(user.Email)
	resp, body := s.verifyTwoFactor(first.PartialToken, "", user.RecoveryCodes[0])
	resp.AssertStatus(200)
	s.getMe(body.AccessToken).AssertOk()

	_, second := s.loginAs(user.Email)
	resp, _ = s.verifyTwoFactor(second.PartialToken, "", user.RecoveryCodes[0])
	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestWrongCodesHitTheAttemptCap() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)

	for i := 0; i < 5; i++ {
		resp, _ := s.verifyTwoFactor(challenge.PartialToken, "000000", "")
		resp.AssertStatus(401)
	}
	resp, _ := s.verifyTwoFactor(challenge.PartialToken, s.currentCode(user.TOTPSecret), "")

	resp.AssertStatus(429)
}

func (s *TwoFactorLoginTestSuite) TestVerifyWithoutAnyCodeIs422() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)

	resp, _ := s.verifyTwoFactor(challenge.PartialToken, "", "")

	resp.AssertStatus(422)
}

func (s *TwoFactorLoginTestSuite) TestUnknownPartialTokenIs401() {
	resp, _ := s.verifyTwoFactor("not-a-challenge", "123456", "")

	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestLoginWithoutTOTPStillReturnsASession() {
	user := s.seedUser(false)

	resp, body := s.loginAs(user.Email)

	resp.AssertStatus(200)
	s.False(body.Requires2FA)
	s.Empty(body.PartialToken)
	s.NotEmpty(body.AccessToken)
	s.NotEmpty(body.RefreshToken)
	s.getMe(body.AccessToken).AssertOk()
}
