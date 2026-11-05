package users

import (
	"testing"
	"time"

	"github.com/goravel/framework/facades"
	"github.com/pquerna/otp/totp"

	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type StepUpTestSuite struct {
	authSuite
}

func TestStepUp_Suite(t *testing.T) {
	support.RunSuite(t, new(StepUpTestSuite))
}

func (s *StepUpTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

type stepUpStatus struct {
	Required  bool    `json:"required"`
	ExpiresAt *string `json:"expires_at"`
}

func (s *StepUpTestSuite) status(bearer string) stepUpStatus {
	s.T().Helper()
	resp := s.Get("/v1/users/me/step-up", support.Session{AccessToken: bearer})
	resp.AssertOk()
	var body stepUpStatus
	s.decode(resp, &body)
	return body
}

// nextCode is the code of the following 30 s step, which the login's code
// (already redeemed) cannot be replayed as.
func (s *StepUpTestSuite) nextCode(secret string) string {
	s.T().Helper()
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	s.Require().NoError(err)
	return code
}

func (s *StepUpTestSuite) signInWithRecovery(email, recoveryCode string) loginBody {
	s.T().Helper()
	_, challenge := s.loginAs(email)
	s.Require().NotEmpty(challenge.ChallengeToken)
	resp, session := s.verifyTwoFactor(challenge.ChallengeToken, "", recoveryCode)
	resp.AssertOk()
	return session
}

func (s *StepUpTestSuite) enableFlag() {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO global_features ("key", enabled, created_at, updated_at) VALUES (?, TRUE, NOW(), NOW())`,
		features.FlagSettings2FARequired,
	)
	s.Require().NoError(err)
}

func (s *StepUpTestSuite) TestStepUp_GrantsAWindowPerSessionAndLogoutEndsIt() {
	user := s.seedUser(true)
	first := s.signInWithRecovery(user.Email, user.RecoveryCodes[0])
	second := s.signInWithRecovery(user.Email, user.RecoveryCodes[1])

	off := s.status(first.AccessToken)
	s.False(off.Required, "flag off: nothing is required")
	s.Nil(off.ExpiresAt)

	s.enableFlag()
	on := s.status(first.AccessToken)
	s.True(on.Required)
	s.Nil(on.ExpiresAt)

	wrong := s.authedPost(first.AccessToken, "/v1/users/me/step-up", `{"totp_code":"000000"}`)
	s.AssertError(wrong, 401, "unauthorized", "invalid 2FA code")
	s.Nil(s.status(first.AccessToken).ExpiresAt)

	granted := s.authedPost(first.AccessToken, "/v1/users/me/step-up", `{"totp_code":"`+s.nextCode(user.TOTPSecret)+`"}`)
	granted.AssertOk()
	var body struct {
		ExpiresAt string `json:"expires_at"`
	}
	s.decode(granted, &body)
	expiresAt, err := time.Parse(time.RFC3339, body.ExpiresAt)
	s.Require().NoError(err)
	s.WithinDuration(time.Now().Add(15*time.Minute), expiresAt, 30*time.Second)

	// Reading the window does not renew it, and another session has none.
	read := s.status(first.AccessToken)
	s.Require().NotNil(read.ExpiresAt)
	s.Equal(body.ExpiresAt, *read.ExpiresAt)
	s.Equal(body.ExpiresAt, *s.status(first.AccessToken).ExpiresAt)
	s.Nil(s.status(second.AccessToken).ExpiresAt, "the window belongs to the session, not the user")

	// Logout ends every session of the user, and with it the window.
	s.authedPost(first.AccessToken, "/v1/auth/logout", "").AssertNoContent()
	s.assertSessionRevoked(s.Get("/v1/users/me/step-up", support.Session{AccessToken: first.AccessToken}))
	s.assertSessionRevoked(s.Get("/v1/users/me/step-up", support.Session{AccessToken: second.AccessToken}))
}

func (s *StepUpTestSuite) TestStepUp_WithoutTwoFactorIsNeverRequired() {
	user := s.seedUser(false)
	session := s.signIn(user.Email)
	s.enableFlag()

	s.False(s.status(session.AccessToken).Required)
	resp := s.authedPost(session.AccessToken, "/v1/users/me/step-up", `{"totp_code":"123456"}`)
	s.AssertError(resp, 401, "unauthorized", "invalid 2FA code")
}

func (s *StepUpTestSuite) TestStepUp_RefusesAnAnonymousCallerAndAMissingCode() {
	s.Get("/v1/users/me/step-up", support.Session{}).AssertUnauthorized()
	s.postJSON("/v1/users/me/step-up", `{"totp_code":"123456"}`).AssertUnauthorized()

	user := s.seedUser(true)
	session := s.signInWithRecovery(user.Email, user.RecoveryCodes[0])
	s.authedPost(session.AccessToken, "/v1/users/me/step-up", `{}`).AssertStatus(422)
}
