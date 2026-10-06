package auth

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/testutil"
)

// AccessStatusTestSuite proves that users.status, account_users.status and
// accounts.status are enforced by sign-in, SessionAuth and the scope
// middlewares.
type AccessStatusTestSuite struct {
	authSuite
}

func TestAccessStatusSuite(t *testing.T) {
	suite.Run(t, new(AccessStatusTestSuite))
}

func (s *AccessStatusTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *AccessStatusTestSuite) setStatus(table string, id uuid.UUID, status string) {
	_, err := facades.Orm().Query().Exec("UPDATE "+table+" SET status = ? WHERE id = ?", status, id)
	s.Require().NoError(err)
}

func (s *AccessStatusTestSuite) seedAccount() uuid.UUID {
	id := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: id, Name: "status-" + id.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	return id
}

func (s *AccessStatusTestSuite) addMember(accountID, userID uuid.UUID, status string) uuid.UUID {
	id := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'owner', ?, NOW(), NOW())`,
		id, accountID, userID, status,
	)
	s.Require().NoError(err)
	return id
}

func (s *AccessStatusTestSuite) send(method, path, bearer string, accountID uuid.UUID, body string) contractstesting.Response {
	req := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+bearer).
		WithHeader("Content-Type", "application/json")
	if accountID != uuid.Nil {
		req = req.WithHeader("X-Account-Id", accountID.String())
	}
	var (
		resp contractstesting.Response
		err  error
	)
	switch method {
	case "GET":
		resp, err = req.Get(path)
	case "POST":
		resp, err = req.Post(path, strings.NewReader(body))
	case "PATCH":
		resp, err = req.Patch(path, strings.NewReader(body))
	default:
		s.FailNow("unsupported method " + method)
	}
	s.Require().NoError(err)
	return resp
}

func (s *AccessStatusTestSuite) assertReadOnlyRefusal(resp contractstesting.Response, status string) {
	resp.AssertStatus(403)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	s.Equal(responses.CodeAccountFrozen, body.Error.Code)
	s.Contains(body.Error.Message, "only reads are allowed")
	s.Equal(status, body.Error.Status)
}

func (s *AccessStatusTestSuite) assertNotAMember(resp contractstesting.Response) {
	resp.AssertStatus(403)
	code, message := s.errorParts(resp)
	s.Equal(responses.CodeForbidden, code)
	s.Equal("not a member of this account", message)
}

func (s *AccessStatusTestSuite) assertAccountMissing(resp contractstesting.Response) {
	resp.AssertStatus(404)
	code, message := s.errorParts(resp)
	s.Equal(responses.CodeNotFound, code)
	s.Equal("account not found", message)
}

func (s *AccessStatusTestSuite) errorParts(resp contractstesting.Response) (string, string) {
	s.T().Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	return body.Error.Code, body.Error.Message
}

func (s *AccessStatusTestSuite) accountName(accountID uuid.UUID) string {
	s.T().Helper()
	var name string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT name FROM accounts WHERE id = ?`, accountID,
	).Scan(&name))
	return name
}

func (s *AccessStatusTestSuite) TestLoginRefusesAUserWhoIsNotActive() {
	for _, status := range []string{"suspended", models.UserStatusInvited} {
		user := s.seedUser(false)
		s.setStatus("users", user.ID, status)

		resp, body := s.loginAs(user.Email)

		resp.AssertStatus(403)
		s.Empty(body.AccessToken, status)
		s.Empty(body.ChallengeToken, status)
	}
}

func (s *AccessStatusTestSuite) TestWrongPasswordStillAnswersInvalidCredentialsForASuspendedUser() {
	user := s.seedUser(false)
	s.setStatus("users", user.ID, "suspended")

	resp := s.postJSON("/v1/auth/login", `{"email":"`+user.Email+`","password":"not-the-password"}`)

	resp.AssertStatus(401)
}

func (s *AccessStatusTestSuite) TestSessionEndsWhenTheUserIsSuspended() {
	user := s.seedUser(false)
	session := s.signIn(user.Email)
	s.getMe(session.AccessToken).AssertOk()

	s.setStatus("users", user.ID, "suspended")

	s.getMe(session.AccessToken).AssertStatus(401)
	resp, renewed := s.refresh(session.RefreshToken)
	resp.AssertStatus(401)
	s.Empty(renewed.AccessToken)
}

func (s *AccessStatusTestSuite) TestTwoFactorCompletionRefusesAUserSuspendedMeanwhile() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)
	s.Require().NotEmpty(challenge.ChallengeToken)
	s.setStatus("users", user.ID, "suspended")

	resp, body := s.verifyTwoFactor(challenge.ChallengeToken, s.currentCode(user.TOTPSecret), "")

	resp.AssertStatus(403)
	s.Empty(body.AccessToken)
}

func (s *AccessStatusTestSuite) TestAMembershipThatIsNotActiveCountsAsAbsent() {
	user := s.seedUser(false)
	accountID := s.seedAccount()
	s.addMember(accountID, user.ID, "suspended")

	_, signedIn := s.loginAs(user.Email)
	s.Require().NotEmpty(signedIn.AccessToken)

	s.Empty(signedIn.AccountID, "a suspended membership is not offered as the default account")
	s.assertNotAMember(s.send("GET", "/v1/accounts/"+accountID.String(), signedIn.AccessToken, uuid.Nil, ""))
	s.assertNotAMember(s.send("GET", "/v1/wallets", signedIn.AccessToken, accountID, ""))
}

func (s *AccessStatusTestSuite) TestAnActiveAccountAcceptsMutations() {
	user := s.seedUser(false)
	accountID := s.seedAccount()
	s.addMember(accountID, user.ID, models.StatusActive)
	session := s.signIn(user.Email)

	s.send("PATCH", "/v1/accounts/"+accountID.String(), session.AccessToken, uuid.Nil, `{"name":"Renamed"}`).AssertOk()
}

func (s *AccessStatusTestSuite) TestFrozenOrArchivedAccountIsReadOnlyOnAccountRoutes() {
	for _, status := range []string{models.AccountStatusFrozen, models.AccountStatusArchived} {
		user := s.seedUser(false)
		accountID := s.seedAccount()
		s.addMember(accountID, user.ID, models.StatusActive)
		session := s.signIn(user.Email)
		s.setStatus("accounts", accountID, status)
		path := "/v1/accounts/" + accountID.String()
		originalName := "status-" + accountID.String()[:8]

		read := s.send("GET", path, session.AccessToken, uuid.Nil, "")
		read.AssertOk()
		var detail struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Error  struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		s.decode(read, &detail)
		s.Equal(originalName, detail.Name)
		s.Equal(status, detail.Status)
		s.Empty(detail.Error.Code)
		s.send("GET", path+"/users", session.AccessToken, uuid.Nil, "").AssertOk()
		s.assertReadOnlyRefusal(s.send("PATCH", path, session.AccessToken, uuid.Nil, `{"name":"Renamed"}`), status)
		s.Equal(originalName, s.accountName(accountID))
		s.assertReadOnlyRefusal(s.send("POST", path+"/tokens", session.AccessToken, uuid.Nil, `{"name":"t"}`), status)

		if status == models.AccountStatusFrozen {
			outsider := s.seedUser(false)
			outsiderSession := s.signIn(outsider.Email)
			s.assertNotAMember(s.send("PATCH", path, outsiderSession.AccessToken, uuid.Nil, `{"name":"Renamed"}`))
			s.Equal(originalName, s.accountName(accountID))
			s.assertAccountMissing(s.send("PATCH", "/v1/accounts/"+uuid.NewString(), session.AccessToken, uuid.Nil, `{"name":"Renamed"}`))
		}
	}
}

func (s *AccessStatusTestSuite) TestFrozenAccountIsReadOnlyThroughTheAccountHeader() {
	user := s.seedUser(false)
	accountID := s.seedAccount()
	s.addMember(accountID, user.ID, models.StatusActive)
	session := s.signIn(user.Email)
	s.setStatus("accounts", accountID, models.AccountStatusFrozen)

	s.send("GET", "/v1/wallets", session.AccessToken, accountID, "").AssertOk()
	s.assertReadOnlyRefusal(s.send("POST", "/v1/wallets", session.AccessToken, accountID, `{}`), models.AccountStatusFrozen)
}

func (s *AccessStatusTestSuite) TestWalletOfAFrozenAccountIsReadOnly() {
	user := s.seedUser(false)
	headerAccount := s.seedAccount()
	walletAccount := s.seedAccount()
	s.addMember(headerAccount, user.ID, models.StatusActive)
	s.addMember(walletAccount, user.ID, models.StatusActive)
	walletID := seedAPIWalletForAccount(s.T(), walletAccount, "eth", "frozen-account-wallet")
	session := s.signIn(user.Email)
	s.setStatus("accounts", walletAccount, models.AccountStatusFrozen)
	path := "/v1/wallets/" + walletID

	s.send("GET", path+"/settings", session.AccessToken, headerAccount, "").AssertOk()
	s.assertReadOnlyRefusal(
		s.send("POST", path+"/whitelist", session.AccessToken, headerAccount, `{"address":"0x0000000000000000000000000000000000000001","label":"x"}`),
		models.AccountStatusFrozen,
	)
}

func (s *AccessStatusTestSuite) TestAPITokenOfAFrozenAccountIsReadOnly() {
	accountID, bearer, _ := support.SetupAPIAuth(s.T(), false)
	walletID := seedAPIWalletForAccount(s.T(), accountID, "eth", "frozen-api-wallet")
	s.setStatus("accounts", accountID, models.AccountStatusFrozen)

	support.Get(s.T(), &s.TestCase, "/api/v1/wallets/"+walletID, bearer).AssertOk()
	s.assertReadOnlyRefusal(
		support.Post(s.T(), &s.TestCase, "/api/v1/wallets/"+walletID+"/addresses", `{"external_user_id":"u1"}`, bearer, nil),
		models.AccountStatusFrozen,
	)
}
