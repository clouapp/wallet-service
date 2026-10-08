package accounts

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

// PlatformAccountLifecycleTestSuite is POST
// /v1/platform/accounts/{accountId}/freeze|unfreeze|archive from S3.4.1.
// accounts.lifecycle is the named permission. A platform_admins row is the
// gate. The posts are not behind AccountContext.
type PlatformAccountLifecycleTestSuite struct {
	authSuite
}

func TestPlatform_Account_LifecycleSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformAccountLifecycleTestSuite))
}

func (s *PlatformAccountLifecycleTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformAccountLifecycleTestSuite) TestPlatform_Admin_FreezesUnfreezesAndArchives() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	adminSession := s.signIn(admin.Email)
	owner := s.seedUser(false)
	ownerSession := s.signIn(owner.Email)
	accountID := s.seedAccount()
	s.addMember(accountID, owner.ID, "owner", models.StatusActive)

	outsider := s.seedUser(false)
	outsiderSession := s.signIn(outsider.Email)
	forbidden := s.post(outsiderSession.AccessToken, "/v1/platform/accounts/"+accountID.String()+"/freeze")
	s.AssertError(forbidden, 403, responses.CodeForbidden, "you do not have permission to change account status")
	s.Equal(models.StatusActive, s.accountStatus(accountID))

	frozen := s.post(adminSession.AccessToken, "/v1/platform/accounts/"+accountID.String()+"/freeze")
	frozen.AssertOk()
	s.Equal(models.AccountStatusFrozen, s.statusOf(frozen))
	s.Equal(models.AccountStatusFrozen, s.accountStatus(accountID))
	again := s.post(adminSession.AccessToken, "/v1/platform/accounts/"+accountID.String()+"/freeze")
	again.AssertOk()
	s.Equal(models.AccountStatusFrozen, s.statusOf(again))

	blocked := s.send("PATCH", "/v1/accounts/"+accountID.String(), ownerSession.AccessToken, `{"name":"renamed"}`)
	s.AssertError(blocked, 403, responses.CodeAccountFrozen, "account is frozen; only reads are allowed")
	s.Equal("lifecycle-"+accountID.String()[:8], s.accountName(accountID))

	unfrozen := s.post(adminSession.AccessToken, "/v1/platform/accounts/"+accountID.String()+"/unfreeze")
	unfrozen.AssertOk()
	s.Equal(models.StatusActive, s.statusOf(unfrozen))
	renamed := s.send("PATCH", "/v1/accounts/"+accountID.String(), ownerSession.AccessToken, `{"name":"renamed"}`)
	renamed.AssertOk()
	s.Equal("renamed", s.accountName(accountID))

	archived := s.post(adminSession.AccessToken, "/v1/platform/accounts/"+accountID.String()+"/archive")
	archived.AssertOk()
	s.Equal(models.AccountStatusArchived, s.statusOf(archived))
	s.Equal(models.AccountStatusArchived, s.accountStatus(accountID))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE target_id = ?`,
		accountID.String(),
	))

	missing := s.post(adminSession.AccessToken, "/v1/platform/accounts/"+uuid.NewString()+"/freeze")
	s.AssertError(missing, 404, responses.CodeNotFound, "account not found")

	badID := s.post(adminSession.AccessToken, "/v1/platform/accounts/not-a-uuid/archive")
	s.AssertError(badID, 400, responses.CodeInvalidRequest, "invalid account id")
}

func (s *PlatformAccountLifecycleTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountLifecycleTestSuite) seedAccount() uuid.UUID {
	id := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: id, Name: "lifecycle-" + id.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	return id
}

func (s *PlatformAccountLifecycleTestSuite) addMember(accountID, userID uuid.UUID, role, status string) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		uuid.New(), accountID, userID, role, status,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountLifecycleTestSuite) post(bearer, path string) contractstesting.Response {
	return s.send("POST", path, bearer, "")
}

func (s *PlatformAccountLifecycleTestSuite) send(method, path, bearer, body string) contractstesting.Response {
	s.T().Helper()
	session := support.Session{AccessToken: bearer}
	switch method {
	case "PATCH":
		return s.Patch(path, session, body)
	default:
		return s.Post(path, session, body)
	}
}

func (s *PlatformAccountLifecycleTestSuite) statusOf(resp contractstesting.Response) string {
	s.T().Helper()
	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	s.decode(resp, &body)
	s.NotEmpty(body.ID)
	return body.Status
}

func (s *PlatformAccountLifecycleTestSuite) errorCode(resp contractstesting.Response) string {
	s.T().Helper()
	return s.errorBody(resp).Code
}

func (s *PlatformAccountLifecycleTestSuite) errorMessage(resp contractstesting.Response) string {
	s.T().Helper()
	return s.errorBody(resp).Message
}

func (s *PlatformAccountLifecycleTestSuite) errorBody(resp contractstesting.Response) struct {
	Code    string `json:"code"`
	Message string `json:"message"`
} {
	s.T().Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	return body.Error
}

func (s *PlatformAccountLifecycleTestSuite) accountStatus(accountID uuid.UUID) string {
	s.T().Helper()
	var status string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT status FROM accounts WHERE id = ?`, accountID,
	).Scan(&status))
	return status
}

func (s *PlatformAccountLifecycleTestSuite) accountName(accountID uuid.UUID) string {
	s.T().Helper()
	var name string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT name FROM accounts WHERE id = ?`, accountID,
	).Scan(&name))
	return name
}

func (s *PlatformAccountLifecycleTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}
