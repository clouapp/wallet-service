package auth

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const authTestNewPassword = "new-correct-horse-battery-staple"

// SessionRevocationTestSuite proves that changing or resetting the password
// and disabling TOTP end the sessions that existed before.
type SessionRevocationTestSuite struct {
	authSuite
}

func TestSession_Revocation_Suite(t *testing.T) {
	suite.Run(t, new(SessionRevocationTestSuite))
}

func (s *SessionRevocationTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *SessionRevocationTestSuite) changePassword(bearer, current, next string) (contractstesting.Response, loginBody) {
	resp := s.authedPost(bearer, "/v1/users/me/password", fmt.Sprintf(
		`{"current_password":%q,"new_password":%q}`, current, next,
	))
	var body loginBody
	s.decode(resp, &body)
	return resp, body
}

func (s *SessionRevocationTestSuite) seedResetToken(userID uuid.UUID) string {
	svc := authsvc.NewService()
	raw, err := svc.GenerateRandomToken()
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		uuid.New(), userID, svc.HashToken(raw), time.Now().Add(time.Hour),
	)
	s.Require().NoError(err)
	return raw
}

func (s *SessionRevocationTestSuite) resetPassword(token string) contractstesting.Response {
	return s.postJSON("/v1/auth/recover/confirm", fmt.Sprintf(`{"token":%q,"new_password":%q}`, token, authTestNewPassword))
}

func (s *SessionRevocationTestSuite) loginWithPassword(email, password string) contractstesting.Response {
	return s.postJSON("/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, password))
}

func (s *SessionRevocationTestSuite) TestChange_Password_EndsEverySessionAndRenewsTheCaller() {
	user := s.seedUser(false)
	caller := s.signIn(user.Email)
	otherDevice := s.signIn(user.Email)

	resp, renewed := s.changePassword(caller.AccessToken, authTestPassword, authTestNewPassword)

	resp.AssertOk()
	s.assertSessionRefused(caller)
	s.assertSessionRefused(otherDevice)
	s.assertSessionWorks(renewed)
	s.loginWithPassword(user.Email, authTestPassword).AssertStatus(401)
	s.loginWithPassword(user.Email, authTestNewPassword).AssertOk()
}

func (s *SessionRevocationTestSuite) TestChange_Password_RefusesTheCallersTokenEvenWithinTheSameSecond() {
	user := s.seedUser(false)
	caller := s.signIn(user.Email)

	resp, renewed := s.changePassword(caller.AccessToken, authTestPassword, authTestNewPassword)

	resp.AssertOk()
	s.getMe(caller.AccessToken).AssertStatus(401)
	s.getMe(renewed.AccessToken).AssertOk()
}

func (s *SessionRevocationTestSuite) TestChange_Password_WithTheWrongPasswordRevokesNothing() {
	user := s.seedUser(false)
	caller := s.signIn(user.Email)

	resp, _ := s.changePassword(caller.AccessToken, "not-the-password", authTestNewPassword)

	resp.AssertStatus(401)
	s.assertSessionWorks(caller)
}

func (s *SessionRevocationTestSuite) TestReset_Password_EndsEverySession() {
	user := s.seedUser(false)
	session := s.signIn(user.Email)
	resetToken := s.seedResetToken(user.ID)

	s.resetPassword(resetToken).AssertOk()

	s.assertSessionRefused(session)
	s.loginWithPassword(user.Email, authTestNewPassword).AssertOk()
}

func (s *SessionRevocationTestSuite) TestReset_Password_RetiresAPendingTwoFactorChallenge() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)
	s.Require().NotEmpty(challenge.ChallengeToken)
	resetToken := s.seedResetToken(user.ID)

	s.resetPassword(resetToken).AssertOk()

	resp, body := s.verifyTwoFactor(challenge.ChallengeToken, s.currentCode(user.TOTPSecret), "")
	resp.AssertStatus(401)
	s.Empty(body.AccessToken)
}

func (s *SessionRevocationTestSuite) TestDisable_TOTP_EndsEverySessionAndRenewsTheCaller() {
	user := s.seedUser(true)
	_, first := s.loginAs(user.Email)
	_, caller := s.verifyTwoFactor(first.ChallengeToken, s.currentCode(user.TOTPSecret), "")
	s.Require().NotEmpty(caller.AccessToken)
	_, second := s.loginAs(user.Email)
	_, otherDevice := s.verifyTwoFactor(second.ChallengeToken, "", user.RecoveryCodes[0])
	s.Require().NotEmpty(otherDevice.AccessToken)

	resp := s.authedDeleteJSON(caller.AccessToken, "/v1/users/me/totp", fmt.Sprintf(
		`{"recovery_code":%q}`, user.RecoveryCodes[1],
	))

	resp.AssertOk()
	var body struct {
		User struct {
			TotpEnabled bool `json:"totp_enabled"`
		} `json:"user"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	s.decode(resp, &body)
	s.False(body.User.TotpEnabled)
	s.assertSessionRefused(caller)
	s.assertSessionRefused(otherDevice)
	s.assertSessionWorks(loginBody{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken})
}

func (s *SessionRevocationTestSuite) TestRefresh_Token_CanOnlyBeRotatedOnce() {
	user := s.seedUser(false)
	session := s.signIn(user.Email)

	first, renewed := s.refresh(session.RefreshToken)
	first.AssertOk()
	second, _ := s.refresh(session.RefreshToken)

	second.AssertStatus(401)
	s.NotEmpty(renewed.RefreshToken)
}

func (s *SessionRevocationTestSuite) TestSign_In_RightAfterAResetGetsAWorkingSession() {
	user := s.seedUser(false)
	s.signIn(user.Email)
	resetToken := s.seedResetToken(user.ID)
	s.resetPassword(resetToken).AssertOk()

	resp := s.loginWithPassword(user.Email, authTestNewPassword).AssertOk()
	var session loginBody
	s.decode(resp, &session)
	s.assertSessionWorks(session)
}
