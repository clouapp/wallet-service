package users

import (
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// PlatformUserSuspendTestSuite is the platform suspension in S3.4.4.
// member.suspended stays the account-member action.
type PlatformUserSuspendTestSuite struct {
	authSuite
}

func TestPlatform_User_SuspendSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformUserSuspendTestSuite))
}

func (s *PlatformUserSuspendTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformUserSuspendTestSuite) TestA_Platform_AdminSuspendsAndTheNextRequestIsForbidden() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	adminSession := s.signIn(admin.Email)

	victim := s.seedUser(false)
	victimSession := s.signIn(victim.Email)
	s.getMe(victimSession.AccessToken).AssertOk()

	first := s.authedPost(adminSession.AccessToken, "/v1/platform/users/"+victim.ID.String()+"/suspend", "")
	first.AssertOk()
	again := s.authedPost(adminSession.AccessToken, "/v1/platform/users/"+victim.ID.String()+"/suspend", "")
	again.AssertOk()
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'user.suspended' AND target_id = ? AND account_id IS NULL`,
		victim.ID.String(),
	))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM users WHERE id = ? AND suspended_at IS NOT NULL AND suspension_reason IS NULL`,
		victim.ID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity a
		 JOIN users u ON u.id::text = a.target_id
		 WHERE a.action IN ('user.suspended', 'user.reactivated')
		   AND a.metadata::text LIKE '%' || u.password_hash || '%'`,
	))

	s.assertSuspended(s.getMe(victimSession.AccessToken))
	loginResp, loginBody := s.loginAs(victim.Email)
	s.assertSuspended(loginResp)
	s.Empty(loginBody.AccessToken)

	refreshResp, refreshBody := s.refresh(victimSession.RefreshToken)
	s.AssertError(refreshResp, 401, "unauthorized", "invalid or expired refresh token")
	s.Empty(refreshBody.AccessToken)

	accountID := s.seedAccount()
	s.addMember(accountID, admin.ID, "owner", models.StatusActive)
	s.addMember(accountID, victim.ID, "user", models.StatusActive)
	patched := s.send("PATCH", "/v1/accounts/"+accountID.String()+"/users/"+victim.ID.String(), adminSession.AccessToken, `{"status":"suspended"}`)
	patched.AssertOk()
	page := s.send("GET", "/v1/accounts/"+accountID.String()+"/activity", adminSession.AccessToken, "")
	page.AssertOk()
	var listed struct {
		Data []struct {
			Action    string `json:"action"`
			AccountID string `json:"account_id"`
		} `json:"data"`
	}
	s.decode(page, &listed)
	seenMember := false
	for _, row := range listed.Data {
		s.NotEqual("user.suspended", row.Action)
		s.NotEqual("user.reactivated", row.Action)
		if row.Action == "member.suspended" {
			seenMember = true
			s.Equal(accountID.String(), row.AccountID)
		}
	}
	s.True(seenMember)

	platform := s.send("GET", "/v1/platform/activity", adminSession.AccessToken, "")
	platform.AssertOk()
	var platformPage struct {
		Data []struct {
			Action    string `json:"action"`
			AccountID string `json:"account_id"`
		} `json:"data"`
	}
	s.decode(platform, &platformPage)
	seenUser := false
	for _, row := range platformPage.Data {
		if row.Action == "user.suspended" {
			seenUser = true
			s.Empty(row.AccountID)
		}
	}
	s.True(seenUser)

	restored := s.authedPost(adminSession.AccessToken, "/v1/platform/users/"+victim.ID.String()+"/reactivate", "")
	restored.AssertOk()
	s.assertSessionRevoked(s.getMe(victimSession.AccessToken))
	signedIn := s.signIn(victim.Email)
	s.getMe(signedIn.AccessToken).AssertOk()
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'user.reactivated' AND target_id = ? AND account_id IS NULL`,
		victim.ID.String(),
	))
}

func (s *PlatformUserSuspendTestSuite) TestA_Member_CannotSuspendAPlatformUser() {
	member := s.seedUser(false)
	victim := s.seedUser(false)
	session := s.signIn(member.Email)

	resp := s.authedPost(session.AccessToken, "/v1/platform/users/"+victim.ID.String()+"/suspend", "")
	resp.AssertForbidden()
	s.AssertError(resp, 403, responses.CodeForbidden, "you do not have permission to suspend users")
	s.Equal(int64(0), s.count(`SELECT count(*) FROM users WHERE id = ? AND suspended_at IS NOT NULL`, victim.ID))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'user.suspended'`))

	missing := s.authedPost(session.AccessToken, "/v1/platform/users/"+uuid.New().String()+"/reactivate", "")
	s.AssertError(missing, 403, responses.CodeForbidden, "you do not have permission to suspend users")
}

func (s *PlatformUserSuspendTestSuite) TestAn_Unknown_UserIsNotFoundForAPlatformAdmin() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	resp := s.authedPost(session.AccessToken, "/v1/platform/users/"+uuid.New().String()+"/suspend", "")
	resp.AssertNotFound()
	s.AssertError(resp, 404, responses.CodeNotFound, "user not found")

	invalid := s.authedPost(session.AccessToken, "/v1/platform/users/not-a-uuid/suspend", "")
	s.AssertError(invalid, 400, "invalid_request", "invalid user id")
}

func (s *PlatformUserSuspendTestSuite) assertSuspended(resp contractstesting.Response) {
	s.T().Helper()
	resp.AssertForbidden()
	s.AssertError(resp, 403, responses.CodeForbidden, responses.SuspendedUserMessage)
}

func (s *PlatformUserSuspendTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformUserSuspendTestSuite) seedAccount() uuid.UUID {
	id := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: id, Name: "suspend-" + id.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	return id
}

func (s *PlatformUserSuspendTestSuite) addMember(accountID, userID uuid.UUID, role, status string) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		uuid.New(), accountID, userID, role, status,
	)
	s.Require().NoError(err)
}

func (s *PlatformUserSuspendTestSuite) send(method, path, bearer, body string) contractstesting.Response {
	s.T().Helper()
	session := support.Session{AccessToken: bearer}
	switch method {
	case "GET":
		return s.Get(path, session)
	case "PATCH":
		return s.Patch(path, session, body)
	default:
		s.FailNow("unsupported method " + method)
		return nil
	}
}

func (s *PlatformUserSuspendTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}
