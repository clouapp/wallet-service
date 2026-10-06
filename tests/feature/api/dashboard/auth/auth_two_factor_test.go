package auth

import (
	"fmt"
	"testing"

	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// TwoFactorLoginTestSuite drives the password + TOTP login over HTTP against
// the real routes, middleware, Postgres and the test Redis index.
type TwoFactorLoginTestSuite struct {
	authSuite
}

func TestTwo_Factor_LoginSuite(t *testing.T) {
	support.RunSuite(t, new(TwoFactorLoginTestSuite))
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

	s.AssertError(resp, 422, "validation_failed", "validation failed")
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, "challenge_token is required to not be empty")
}

func (s *TwoFactorLoginTestSuite) TestSession_Auth_RejectsThePartialToken() {
	user := s.seedUser(true)
	_, body := s.loginAs(user.Email)
	s.Require().NotEmpty(body.ChallengeToken)

	s.AssertError(s.getMe(body.ChallengeToken), 401, "unauthorized", "invalid token")

	resp := s.Get("/v1/wallets", support.Session{AccessToken: body.ChallengeToken, AccountID: user.ID.String()})
	s.AssertError(resp, 401, "unauthorized", "invalid token")
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

	s.AssertError(resp, 401, "unauthorized", "invalid or expired partial token")
}

func (s *TwoFactorLoginTestSuite) TestReplayed_Code_IsRefused() {
	user := s.seedUser(true)
	code := s.currentCode(user.TOTPSecret)
	_, first := s.loginAs(user.Email)
	resp, _ := s.verifyTwoFactor(first.ChallengeToken, code, "")
	resp.AssertStatus(200)

	_, second := s.loginAs(user.Email)
	resp, _ = s.verifyTwoFactor(second.ChallengeToken, code, "")

	s.AssertError(resp, 401, "unauthorized", "invalid 2FA code")
}

func (s *TwoFactorLoginTestSuite) TestRecovery_Code_StillWorksAndIsSingleUse() {
	user := s.seedUser(true)

	_, first := s.loginAs(user.Email)
	resp, body := s.verifyTwoFactor(first.ChallengeToken, "", user.RecoveryCodes[0])
	resp.AssertStatus(200)
	s.getMe(body.AccessToken).AssertOk()

	_, second := s.loginAs(user.Email)
	resp, _ = s.verifyTwoFactor(second.ChallengeToken, "", user.RecoveryCodes[0])
	s.AssertError(resp, 401, "unauthorized", "invalid 2FA code")
}

func (s *TwoFactorLoginTestSuite) TestWrong_Codes_HitTheAttemptCap() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)

	for i := 0; i < 5; i++ {
		resp, _ := s.verifyTwoFactor(challenge.ChallengeToken, "000000", "")
		s.AssertError(resp, 401, "unauthorized", "invalid 2FA code")
	}
	resp, _ := s.verifyTwoFactor(challenge.ChallengeToken, s.currentCode(user.TOTPSecret), "")

	s.AssertError(resp, 429, "too_many_requests", "too many 2FA attempts, sign in again later")
}

func (s *TwoFactorLoginTestSuite) TestVerify_Without_AnyCodeIs422() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)

	resp, _ := s.verifyTwoFactor(challenge.ChallengeToken, "", "")

	s.AssertError(resp, 422, "unprocessable", "code or recovery_code is required")
}

func (s *TwoFactorLoginTestSuite) TestUnknown_Partial_TokenIs401() {
	resp, _ := s.verifyTwoFactor("not-a-challenge", "123456", "")

	s.AssertError(resp, 401, "unauthorized", "invalid or expired partial token")
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
