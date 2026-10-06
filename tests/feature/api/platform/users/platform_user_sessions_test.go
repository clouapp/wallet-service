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

// PlatformUserSessionsTestSuite is POST /v1/platform/users/{id}/sessions/revoke
// from S3.4.1. member.suspended stays the account-member action.
type PlatformUserSessionsTestSuite struct {
	authSuite
}

func TestPlatform_User_SessionsSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformUserSessionsTestSuite))
}

func (s *PlatformUserSessionsTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformUserSessionsTestSuite) TestA_Platform_AdminRevokesSessionsWithoutSuspending() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	adminSession := s.signIn(admin.Email)

	victim := s.seedUser(true)
	_, first := s.loginAs(victim.Email)
	_, session := s.verifyTwoFactor(first.ChallengeToken, s.currentCode(victim.TOTPSecret), "")
	s.Require().NotEmpty(session.AccessToken)
	_, challenge := s.loginAs(victim.Email)
	s.Require().NotEmpty(challenge.ChallengeToken)
	s.getMe(session.AccessToken).AssertOk()

	path := "/v1/platform/users/" + victim.ID.String() + "/sessions/revoke"
	s.authedPost(adminSession.AccessToken, path, "").AssertStatus(204)
	s.authedPost(adminSession.AccessToken, path, "").AssertStatus(204)

	s.Equal(int64(2), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'user.sessions_revoked' AND target_id = ? AND account_id IS NULL AND actor_user_id = ?`,
		victim.ID.String(), admin.ID,
	))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM users WHERE id = ? AND sessions_revoked_at IS NOT NULL AND suspended_at IS NULL`,
		victim.ID,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'member.suspended'`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'user.suspended'`))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity a
		 JOIN users u ON u.id::text = a.target_id
		 WHERE a.action = 'user.sessions_revoked'
		   AND a.metadata::text LIKE '%' || u.password_hash || '%'`,
	))

	s.assertSessionRefused(session)
	verify, body := s.verifyTwoFactor(challenge.ChallengeToken, "", victim.RecoveryCodes[0])
	verify.AssertStatus(401)
	s.Empty(body.AccessToken)

	s.getMe(adminSession.AccessToken).AssertOk()
	_, login := s.loginAs(victim.Email)
	_, renewed := s.verifyTwoFactor(login.ChallengeToken, "", victim.RecoveryCodes[1])
	s.Require().NotEmpty(renewed.AccessToken)
	s.getMe(renewed.AccessToken).AssertOk()

	platform := s.platformActivity(adminSession.AccessToken)
	platform.AssertOk()
	var listed struct {
		Data []struct {
			Action    string `json:"action"`
			AccountID string `json:"account_id"`
		} `json:"data"`
	}
	s.decode(platform, &listed)
	seen := 0
	for _, row := range listed.Data {
		s.NotEqual("member.suspended", row.Action)
		if row.Action == "user.sessions_revoked" {
			seen++
			s.Empty(row.AccountID)
		}
	}
	s.Equal(2, seen)
}

func (s *PlatformUserSessionsTestSuite) TestA_Member_CannotRevokePlatformSessions() {
	member := s.seedUser(false)
	victim := s.seedUser(false)
	session := s.signIn(member.Email)
	victimSession := s.signIn(victim.Email)

	resp := s.authedPost(session.AccessToken, "/v1/platform/users/"+victim.ID.String()+"/sessions/revoke", "")
	resp.AssertForbidden()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	s.Equal(responses.CodeForbidden, body.Error.Code)
	s.Equal("you do not have permission to revoke user sessions", body.Error.Message)
	s.Equal(int64(0), s.count(`SELECT count(*) FROM users WHERE id = ? AND sessions_revoked_at IS NOT NULL`, victim.ID))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'user.sessions_revoked'`))
	s.assertSessionWorks(victimSession)

	missing := s.authedPost(session.AccessToken, "/v1/platform/users/"+uuid.New().String()+"/sessions/revoke", "")
	missing.AssertForbidden()
}

func (s *PlatformUserSessionsTestSuite) TestAn_Unknown_UserIsNotFound() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	resp := s.authedPost(session.AccessToken, "/v1/platform/users/"+uuid.New().String()+"/sessions/revoke", "")
	resp.AssertNotFound()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	s.Equal(responses.CodeNotFound, body.Error.Code)
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'user.sessions_revoked'`))

	invalid := s.authedPost(session.AccessToken, "/v1/platform/users/not-a-uuid/sessions/revoke", "")
	invalid.AssertBadRequest()
}

func (s *PlatformUserSessionsTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformUserSessionsTestSuite) platformActivity(bearer string) contractstesting.Response {
	s.T().Helper()
	resp := s.Get("/v1/platform/activity", support.Session{AccessToken: bearer})
	return resp
}

func (s *PlatformUserSessionsTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}
