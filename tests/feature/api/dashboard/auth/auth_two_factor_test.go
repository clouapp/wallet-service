package auth

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// TwoFactorLoginTestSuite drives the password + TOTP login over HTTP against
// the real routes, middleware, Postgres and the test Redis index.
type TwoFactorLoginTestSuite struct {
	authSuite
}

func TestTwo_Factor_LoginSuite(t *testing.T) {
	suite.Run(t, new(TwoFactorLoginTestSuite))
}

func (s *TwoFactorLoginTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *TwoFactorLoginTestSuite) TestLogin_With_TOTPReturnsAChallengeAndNoSession() {
	user := s.seedUser(true)

	resp, body := s.loginAs(user.Email)

	resp.AssertStatus(200)
	s.True(body.Requires2FA)
	s.NotEmpty(body.ChallengeToken)
	s.Positive(body.ExpiresIn)
	s.Empty(body.AccessToken, "the password alone must not earn a session")
	s.Empty(body.RefreshToken)
	raw, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(raw, `"challenge_token"`)
	s.NotContains(raw, `"partial_token"`)
}

func (s *TwoFactorLoginTestSuite) TestVerify_Rejects_TheOldPartialTokenField() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)

	resp := s.postJSON("/v1/auth/2fa/verify", fmt.Sprintf(
		`{"partial_token":%q,"code":"000000"}`, challenge.ChallengeToken,
	))

	resp.AssertStatus(422)
}

func (s *TwoFactorLoginTestSuite) TestSession_Auth_RejectsThePartialToken() {
	user := s.seedUser(true)
	_, body := s.loginAs(user.Email)
	s.Require().NotEmpty(body.ChallengeToken)

	s.getMe(body.ChallengeToken).AssertStatus(401)

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+body.ChallengeToken).
		WithHeader("X-Account-Id", user.ID.String()).
		Get("/v1/wallets")
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestValid_TOTP_CompletesTheLogin() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)

	resp, body := s.verifyTwoFactor(challenge.ChallengeToken, s.currentCode(user.TOTPSecret), "")

	resp.AssertStatus(200)
	s.Require().NotEmpty(body.AccessToken)
	s.NotEmpty(body.RefreshToken)
	s.getMe(body.AccessToken).AssertOk()
}

func (s *TwoFactorLoginTestSuite) TestPartial_Token_IsSingleUse() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)
	resp, _ := s.verifyTwoFactor(challenge.ChallengeToken, s.currentCode(user.TOTPSecret), "")
	resp.AssertStatus(200)

	resp, _ = s.verifyTwoFactor(challenge.ChallengeToken, "", user.RecoveryCodes[0])

	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestReplayed_Code_IsRefused() {
	user := s.seedUser(true)
	code := s.currentCode(user.TOTPSecret)
	_, first := s.loginAs(user.Email)
	resp, _ := s.verifyTwoFactor(first.ChallengeToken, code, "")
	resp.AssertStatus(200)

	_, second := s.loginAs(user.Email)
	resp, _ = s.verifyTwoFactor(second.ChallengeToken, code, "")

	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestRecovery_Code_StillWorksAndIsSingleUse() {
	user := s.seedUser(true)

	_, first := s.loginAs(user.Email)
	resp, body := s.verifyTwoFactor(first.ChallengeToken, "", user.RecoveryCodes[0])
	resp.AssertStatus(200)
	s.getMe(body.AccessToken).AssertOk()

	_, second := s.loginAs(user.Email)
	resp, _ = s.verifyTwoFactor(second.ChallengeToken, "", user.RecoveryCodes[0])
	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestWrong_Codes_HitTheAttemptCap() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)

	for i := 0; i < 5; i++ {
		resp, _ := s.verifyTwoFactor(challenge.ChallengeToken, "000000", "")
		resp.AssertStatus(401)
	}
	resp, _ := s.verifyTwoFactor(challenge.ChallengeToken, s.currentCode(user.TOTPSecret), "")

	resp.AssertStatus(429)
}

func (s *TwoFactorLoginTestSuite) TestVerify_Without_AnyCodeIs422() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)

	resp, _ := s.verifyTwoFactor(challenge.ChallengeToken, "", "")

	resp.AssertStatus(422)
}

func (s *TwoFactorLoginTestSuite) TestUnknown_Partial_TokenIs401() {
	resp, _ := s.verifyTwoFactor("not-a-challenge", "123456", "")

	resp.AssertStatus(401)
}

func (s *TwoFactorLoginTestSuite) TestLogin_Without_TOTPStillReturnsASession() {
	user := s.seedUser(false)

	resp, body := s.loginAs(user.Email)

	resp.AssertStatus(200)
	s.False(body.Requires2FA)
	s.Empty(body.ChallengeToken)
	s.Require().NotEmpty(body.AccessToken)
	s.NotEmpty(body.RefreshToken)
	s.getMe(body.AccessToken).AssertOk()
}
