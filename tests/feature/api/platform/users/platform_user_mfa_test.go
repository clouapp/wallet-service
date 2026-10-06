package users

import (
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// PlatformUserMFATestSuite is DELETE /v1/platform/users/{id}/mfa from S3.4.1.
// users.mfa.reset is the named permission; a platform_admins row is the gate.
type PlatformUserMFATestSuite struct {
	authSuite
}

func TestPlatform_User_MFASuite(t *testing.T) {
	support.RunSuite(t, new(PlatformUserMFATestSuite))
}

func (s *PlatformUserMFATestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformUserMFATestSuite) TestA_Platform_AdminClearsTotpWithoutSuspendingAndRevokesSessions() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	adminSession := s.signIn(admin.Email)

	victim := s.seedUser(true)
	_, first := s.loginAs(victim.Email)
	_, session := s.verifyTwoFactor(first.ChallengeToken, s.currentCode(victim.TOTPSecret), "")
	s.Require().NotEmpty(session.AccessToken)
	s.getMe(session.AccessToken).AssertOk()

	path := "/v1/platform/users/" + victim.ID.String() + "/mfa"
	firstReset := s.authedDelete(adminSession.AccessToken, path)
	firstReset.AssertStatus(204)
	s.emptyBody(firstReset)
	again := s.authedDelete(adminSession.AccessToken, path)
	again.AssertStatus(204)

	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'user.mfa_reset' AND target_id = ? AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = '{"enabled":false,"key":"totp"}'::jsonb`,
		victim.ID.String(), admin.ID,
	))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM users
		 WHERE id = ? AND totp_enabled = FALSE
		   AND suspended_at IS NULL AND sessions_revoked_at IS NOT NULL`,
		victim.ID,
	))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM mfa_credentials
		 WHERE subject_type = 'users' AND subject_id = ? AND secret = ''`,
		victim.ID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM mfa_backup_codes WHERE subject_type = 'users' AND subject_id = ?`, victim.ID,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'user.suspended'`))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'user.sessions_revoked' AND target_id = ? AND account_id IS NULL AND actor_user_id = ?`,
		victim.ID.String(), admin.ID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'user.mfa_reset'
		   AND (metadata::text LIKE '%totp_secret%' OR metadata::text LIKE '%code_hash%' OR metadata::text LIKE '%recovery%')`,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM activity_log
		 WHERE properties::text LIKE '%totp_secret%' OR properties::text LIKE '%code_hash%'`,
	))

	s.assertSessionRefused(session)
	login, body := s.loginAs(victim.Email)
	login.AssertOk()
	s.False(body.Requires2FA)
	s.NotEmpty(body.AccessToken)

	clear := s.seedUser(false)
	s.authedDelete(adminSession.AccessToken, "/v1/platform/users/"+clear.ID.String()+"/mfa").AssertStatus(204)
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'user.mfa_reset' AND target_id = ?`,
		clear.ID.String(),
	))

	platform := s.platformActivity(adminSession.AccessToken)
	platform.AssertOk()
	var listed struct {
		Data []struct {
			Action    string `json:"action"`
			AccountID string `json:"account_id"`
			Metadata  struct {
				Key     string `json:"key"`
				Enabled *bool  `json:"enabled"`
			} `json:"metadata"`
		} `json:"data"`
	}
	s.decode(platform, &listed)
	seen := 0
	for _, row := range listed.Data {
		if row.Action != "user.mfa_reset" {
			continue
		}
		seen++
		s.Empty(row.AccountID)
		s.Equal("totp", row.Metadata.Key)
		s.Require().NotNil(row.Metadata.Enabled)
		s.False(*row.Metadata.Enabled)
	}
	s.Equal(1, seen)
}

func (s *PlatformUserMFATestSuite) TestA_Member_CannotResetPlatformMFA() {
	member := s.seedUser(false)
	victim := s.seedUser(true)
	session := s.signIn(member.Email)
	victimSession := s.signInWithoutUsingTheSecret(victim)

	resp := s.authedDelete(session.AccessToken, "/v1/platform/users/"+victim.ID.String()+"/mfa")
	resp.AssertForbidden()
	s.AssertError(resp, 403, responses.CodeForbidden, "you do not have permission to reset user mfa")
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM users WHERE id = ? AND totp_enabled = TRUE AND suspended_at IS NULL`,
		victim.ID,
	))
	s.Equal(int64(2), s.count(
		`SELECT count(*) FROM mfa_backup_codes WHERE subject_type = 'users' AND subject_id = ?`, victim.ID,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'user.mfa_reset'`))
	s.assertSessionWorks(victimSession)

	missing := s.authedDelete(session.AccessToken, "/v1/platform/users/"+uuid.New().String()+"/mfa")
	missing.AssertForbidden()
}

func (s *PlatformUserMFATestSuite) TestAn_Unknown_UserIsNotFound() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	resp := s.authedDelete(session.AccessToken, "/v1/platform/users/"+uuid.New().String()+"/mfa")
	resp.AssertNotFound()
	s.AssertError(resp, 404, responses.CodeNotFound, "user not found")
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'user.mfa_reset'`))

	invalid := s.authedDelete(session.AccessToken, "/v1/platform/users/not-a-uuid/mfa")
	invalid.AssertBadRequest()
}

func (s *PlatformUserMFATestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformUserMFATestSuite) platformActivity(bearer string) contractstesting.Response {
	s.T().Helper()
	resp := s.Get("/v1/platform/activity", support.Session{AccessToken: bearer})
	return resp
}

func (s *PlatformUserMFATestSuite) emptyBody(resp contractstesting.Response) {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Empty(content)
}

func (s *PlatformUserMFATestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

// signInWithoutUsingTheSecret starts a session for a user who has TOTP
// without putting the secret into an assertion.
func (s *PlatformUserMFATestSuite) signInWithoutUsingTheSecret(user seededAuthUser) loginBody {
	s.T().Helper()
	_, first := s.loginAs(user.Email)
	_, session := s.verifyTwoFactor(first.ChallengeToken, s.currentCode(user.TOTPSecret), "")
	s.NotEmpty(session.AccessToken)
	return session
}
