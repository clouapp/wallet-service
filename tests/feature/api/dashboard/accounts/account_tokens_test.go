package accounts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const accountTokenPassword = "correct-horse-battery"

// accountTokensSuite checks S3.4.6's mint rule: permissions is a JSON array
// from the API catalog, and the creator cannot grant a permission the role
// does not hold. The suite never prints the minted token.
type accountTokensSuite struct {
	support.HTTPSuite
}

func TestAccount_Token_Permissions(t *testing.T) {
	support.RunSuite(t, new(accountTokensSuite))
}

func (s *accountTokensSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *accountTokensSuite) TestOwner_Mints_ACatalogSubset() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"ci","permissions":["wallets.read","webhooks.write"]}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))

	var parsed struct {
		Token    string `json:"token"`
		Metadata struct {
			Name        string   `json:"name"`
			Permissions []string `json:"permissions"`
		} `json:"metadata"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	if parsed.Token == "" {
		s.Fail("mint returned an empty token")
	}
	s.Equal("ci", parsed.Metadata.Name)
	s.Equal([]string{"wallets.read", "webhooks.write"}, parsed.Metadata.Permissions)
	s.JSONEq(`["wallets.read","webhooks.write"]`, s.storedPermissions(accountID, "ci"))
}

func (s *accountTokensSuite) TestOmitted_Permissions_StayEmpty() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"plain"}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.False(s.metadataHasPermissions(resp))
	s.Equal("", s.storedPermissions(accountID, "plain"))
}

func (s *accountTokensSuite) TestEmpty_Permission_ListStaysEmpty() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"empty","permissions":[]}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.False(s.metadataHasPermissions(resp))
	s.Equal("", s.storedPermissions(accountID, "empty"))
}

func (s *accountTokensSuite) TestName_Outside_TheCatalogIsRejected() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"bad","permissions":["wallets:read"]}`)
	s.Equal(http.StatusUnprocessableEntity, s.statusOf(resp))
	s.Equal(int64(0), s.tokenCount(accountID))

	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Errors map[string][]string `json:"errors"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("validation_failed", parsed.Error.Code)
	s.NotEmpty(parsed.Errors["permissions.*"])
}

func (s *accountTokensSuite) TestBlank_Spending_LimitStaysEmptyObject() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"blank-cap","spending_limit":{}}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.Equal("{}", s.storedSpendingLimit(accountID, "blank-cap"))
}

func (s *accountTokensSuite) TestDaily_USD_SpendingLimitIsStored() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"capped","spending_limit":{"daily_usd":"12.50"}}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.Equal(`{"daily_usd":"12.50"}`, s.storedSpendingLimit(accountID, "capped"))
}

func (s *accountTokensSuite) TestNegative_Spending_LimitIsNotStored() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"negative","spending_limit":{"daily_usd":"-1"}}`)
	s.Equal(http.StatusUnprocessableEntity, s.statusOf(resp))
	s.Equal(int64(0), s.tokenCount(accountID))
	s.Equal(int64(0), s.activityCount(accountID, activitylog.ActionTokenCreated))

	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Errors map[string][]string `json:"errors"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("validation_failed", parsed.Error.Code)
	s.NotEmpty(parsed.Errors["spending_limit.daily_usd"])
}

func (s *accountTokensSuite) TestAuditor_Can_ListAndCannotMint() {
	accountID := s.createAccount()
	s.loginUser("owner", accountID)
	auditor := s.loginUser("auditor", accountID)

	list := s.Get("/v1/accounts/"+accountID.String()+"/tokens", support.Session{AccessToken: auditor.token})
	s.Equal(http.StatusOK, s.statusOf(list))

	resp := s.createToken(auditor.token, accountID, `{"name":"nope"}`)
	s.assertCreateForbidden(resp)
	s.Equal(int64(0), s.tokenCount(accountID))
}

func (s *accountTokensSuite) TestUser_Cannot_ListTokens() {
	accountID := s.createAccount()
	s.loginUser("owner", accountID)
	user := s.loginUser("user", accountID)

	list := s.Get("/v1/accounts/"+accountID.String()+"/tokens", support.Session{AccessToken: user.token})
	s.Equal(http.StatusForbidden, s.statusOf(list))
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Data json.RawMessage `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(list)), &parsed))
	s.Equal("forbidden", parsed.Error.Code)
	s.Equal("forbidden", parsed.Error.Message)
	s.Empty(parsed.Data)
}

func (s *accountTokensSuite) TestBlank_IP_CidrIsStoredEmpty() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"open"}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.Equal("", s.storedIPCidr(accountID, "open"))
}

func (s *accountTokensSuite) TestIP_Cidr_IsStored() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"locked","ip_cidr":" 192.0.2.0/24 "}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.Equal("192.0.2.0/24", s.storedIPCidr(accountID, "locked"))
}

func (s *accountTokensSuite) TestInvalid_IP_CidrIs422() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"bad-cidr","ip_cidr":"not-a-cidr"}`)
	s.Equal(http.StatusUnprocessableEntity, s.statusOf(resp))
	s.Equal(int64(0), s.tokenCount(accountID))

	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Errors map[string][]string `json:"errors"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("validation_failed", parsed.Error.Code)
	s.Equal("validation failed", parsed.Error.Message)
	s.NotEmpty(parsed.Errors["ip_cidr"])
}

func (s *accountTokensSuite) TestCreate_Stores_OnlyTheSecretHash() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"sealed"}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))

	var parsed struct {
		Token    string                     `json:"token"`
		Metadata map[string]json.RawMessage `json:"metadata"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	claims := &middleware.APITokenClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(parsed.Token, claims)
	s.Require().NoError(err)
	if claims.Secret == "" {
		s.Fail("create response omitted the one-time secret")
	}
	stored := s.storedHash(accountID, "sealed")
	if stored == claims.Secret || !authsvc.APITokenSecretMatches(claims.Secret, stored) {
		s.Fail("token_hash is not the sha256 of the secret")
	}
	if _, onWire := parsed.Metadata["token_hash"]; onWire {
		s.Fail("token hash is on the create response")
	}
	s.assertAbsent(s.body(resp), stored, "stored digest")

	list := s.listTokens(owner.token, accountID)
	s.Equal(http.StatusOK, s.statusOf(list))
	s.assertAbsent(s.body(list), claims.Secret, "secret")
	s.assertAbsent(s.body(list), stored, "stored digest")

	external := s.External("/api/v1/chains", support.Token{Bearer: parsed.Token}).Get()
	if s.statusOf(external) == http.StatusUnauthorized {
		s.Fail("minted token was rejected")
	}
}

func (s *accountTokensSuite) TestCreate_And_RevokeWriteActivityWithoutTheSecret() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"audited","permissions":["webhooks.write","wallets.read"],"spending_limit":{"daily_usd":"3.00"}}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))

	var parsed struct {
		Token    string `json:"token"`
		Metadata struct {
			ID string `json:"id"`
		} `json:"metadata"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	claims := &middleware.APITokenClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(parsed.Token, claims)
	s.Require().NoError(err)
	if claims.Secret == "" {
		s.Fail("create response omitted the one-time secret")
	}
	stored := s.storedHash(accountID, "audited")

	created := s.activityBody(owner.token, accountID)
	s.Contains(created, activitylog.ActionTokenCreated)
	s.Contains(created, `"name":"audited"`)
	s.Contains(created, `"wallets.read"`)
	s.Contains(created, `"webhooks.write"`)
	s.NotContains(created, "daily_usd")
	s.assertAbsent(created, claims.Secret, "secret")
	s.assertAbsent(created, stored, "stored digest")
	s.Equal(int64(1), s.activityCount(accountID, activitylog.ActionTokenCreated))

	revoked := s.revokeToken(owner.token, accountID, parsed.Metadata.ID)
	s.Equal(http.StatusNoContent, s.statusOf(revoked))
	again := s.revokeToken(owner.token, accountID, parsed.Metadata.ID)
	s.Equal(http.StatusNoContent, s.statusOf(again))
	s.Equal(int64(1), s.activityCount(accountID, activitylog.ActionTokenRevoked))

	listed := s.activityBody(owner.token, accountID)
	s.Contains(listed, activitylog.ActionTokenRevoked)
	s.NotContains(listed, "daily_usd")
	s.assertAbsent(listed, claims.Secret, "secret")
	s.assertAbsent(listed, stored, "stored digest")
}

func (s *accountTokensSuite) TestUser_Cannot_Mint() {
	accountID := s.createAccount()
	s.loginUser("owner", accountID)
	user := s.loginUser("user", accountID)

	resp := s.createToken(user.token, accountID, `{"name":"nope","permissions":["wallets.create"]}`)
	s.assertCreateForbidden(resp)
	s.Equal(int64(0), s.tokenCount(accountID))
}

func (s *accountTokensSuite) TestMint_Scopes_StayWithTheRole() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)
	admin := s.loginUser("admin", accountID)
	auditor := s.loginUser("auditor", accountID)
	user := s.loginUser("user", accountID)

	ownerResp := s.createToken(owner.token, accountID, `{"name":"owner-scope","permissions":["sweep.execute","transactions.read"]}`)
	s.Equal(http.StatusCreated, s.statusOf(ownerResp))
	s.JSONEq(`["sweep.execute","transactions.read"]`, s.storedPermissions(accountID, "owner-scope"))

	adminResp := s.createToken(admin.token, accountID, `{"name":"admin-scope","permissions":["wallets.create","webhooks.write"]}`)
	s.Equal(http.StatusCreated, s.statusOf(adminResp))
	s.JSONEq(`["wallets.create","webhooks.write"]`, s.storedPermissions(accountID, "admin-scope"))

	// The mint policy allows these scopes. tokens.write still refuses the create.
	userHeld := s.createToken(user.token, accountID, `{"name":"user-held","permissions":["wallets.read","addresses.create"]}`)
	s.assertCreateForbidden(userHeld)
	userDenied := s.createToken(user.token, accountID, `{"name":"user-denied","permissions":["wallets.create"]}`)
	s.assertCreateForbidden(userDenied)

	auditorHeld := s.createToken(auditor.token, accountID, `{"name":"auditor-held","permissions":["transactions.read","webhooks.read"]}`)
	s.assertCreateForbidden(auditorHeld)
	auditorDenied := s.createToken(auditor.token, accountID, `{"name":"auditor-denied","permissions":["sweep.execute"]}`)
	s.assertCreateForbidden(auditorDenied)

	s.Equal(int64(2), s.tokenCount(accountID))
}

func (s *accountTokensSuite) TestAdmin_Can_Mint() {
	accountID := s.createAccount()
	admin := s.loginUser("admin", accountID)

	resp := s.createToken(admin.token, accountID, `{"name":"admin-mint"}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.Equal(int64(1), s.tokenCount(accountID))
}

func (s *accountTokensSuite) TestAdmin_Can_Revoke() {
	accountID := s.createAccount()
	admin := s.loginUser("admin", accountID)

	resp := s.createToken(admin.token, accountID, `{"name":"admin-revoke"}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	revoked := s.revokeToken(admin.token, accountID, s.createdTokenID(resp))
	s.Equal(http.StatusNoContent, s.statusOf(revoked))
	s.True(s.tokenRevoked(accountID, "admin-revoke"))
}

func (s *accountTokensSuite) TestAuditor_Cannot_Revoke() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)
	auditor := s.loginUser("auditor", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"keep-auditor"}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	denied := s.revokeToken(auditor.token, accountID, s.createdTokenID(resp))
	s.assertCreateForbidden(denied)
	s.False(s.tokenRevoked(accountID, "keep-auditor"))
	s.Equal(int64(0), s.activityCount(accountID, activitylog.ActionTokenRevoked))
}

func (s *accountTokensSuite) TestUser_Cannot_Revoke() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)
	user := s.loginUser("user", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"keep-user"}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	denied := s.revokeToken(user.token, accountID, s.createdTokenID(resp))
	s.assertCreateForbidden(denied)
	s.False(s.tokenRevoked(accountID, "keep-user"))
	s.Equal(int64(0), s.activityCount(accountID, activitylog.ActionTokenRevoked))
}

func (s *accountTokensSuite) createAccount() uuid.UUID {
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Tokens " + accountID.String()[:8], Status: "active", Environment: "prod",
	}))
	return accountID
}

func (s *accountTokensSuite) loginUser(role string, accountID uuid.UUID) struct {
	id    uuid.UUID
	token string
} {
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	hash, err := authsvc.NewService().HashPassword(accountTokenPassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
	return struct {
		id    uuid.UUID
		token string
	}{id: userID, token: s.login(email)}
}

func (s *accountTokensSuite) login(email string) string {
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountTokenPassword)
	resp := s.Post("/v1/auth/login", support.Session{}, body)
	s.Equal(http.StatusOK, s.statusOf(resp))
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	if parsed.AccessToken == "" {
		s.Fail("login returned an empty session")
	}
	return parsed.AccessToken
}

func (s *accountTokensSuite) createToken(token string, accountID uuid.UUID, body string) contractstesting.Response {
	resp := s.Post("/v1/accounts/"+accountID.String()+"/tokens", support.Session{AccessToken: token}, body)
	return resp
}

func (s *accountTokensSuite) metadataHasPermissions(resp contractstesting.Response) bool {
	var parsed struct {
		Metadata map[string]json.RawMessage `json:"metadata"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	_, ok := parsed.Metadata["permissions"]
	return ok
}

func (s *accountTokensSuite) revokeToken(token string, accountID uuid.UUID, tokenID string) contractstesting.Response {
	resp := s.Delete("/v1/accounts/"+accountID.String()+"/tokens/"+tokenID, support.Session{AccessToken: token}, "")
	return resp
}

func (s *accountTokensSuite) activityBody(token string, accountID uuid.UUID) string {
	resp := s.Get("/v1/accounts/"+accountID.String()+"/activity", support.Session{AccessToken: token})
	s.Equal(http.StatusOK, s.statusOf(resp))
	return s.body(resp)
}

func (s *accountTokensSuite) activityCount(accountID uuid.UUID, action string) int64 {
	total, err := facades.Orm().Query().Model(&models.AccountActivity{}).
		Where("account_id = ? AND action = ?", accountID, action).
		Count()
	s.Require().NoError(err)
	return total
}

func (s *accountTokensSuite) listTokens(token string, accountID uuid.UUID) contractstesting.Response {
	resp := s.Get("/v1/accounts/"+accountID.String()+"/tokens", support.Session{AccessToken: token})
	return resp
}

func (s *accountTokensSuite) storedHash(accountID uuid.UUID, name string) string {
	var token models.AccessToken
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND name = ?", accountID, name).
		First(&token))
	return token.TokenHash
}

func (s *accountTokensSuite) assertAbsent(body, hidden, label string) {
	s.T().Helper()
	if hidden != "" && strings.Contains(body, hidden) {
		s.Fail(label + " appeared in a response")
	}
}

func (s *accountTokensSuite) storedSpendingLimit(accountID uuid.UUID, name string) string {
	var token models.AccessToken
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND name = ?", accountID, name).
		First(&token))
	return token.SpendingLimit
}

func (s *accountTokensSuite) storedIPCidr(accountID uuid.UUID, name string) string {
	var token models.AccessToken
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND name = ?", accountID, name).
		First(&token))
	return token.IpCidr
}

func (s *accountTokensSuite) storedPermissions(accountID uuid.UUID, name string) string {
	var token models.AccessToken
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND name = ?", accountID, name).
		First(&token))
	return token.Permissions
}

func (s *accountTokensSuite) tokenCount(accountID uuid.UUID) int64 {
	total, err := facades.Orm().Query().Model(&models.AccessToken{}).
		Where("account_id = ?", accountID).
		Count()
	s.Require().NoError(err)
	return total
}

func (s *accountTokensSuite) createdTokenID(resp contractstesting.Response) string {
	var parsed struct {
		Metadata struct {
			ID string `json:"id"`
		} `json:"metadata"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Require().NotEmpty(parsed.Metadata.ID)
	return parsed.Metadata.ID
}

func (s *accountTokensSuite) tokenRevoked(accountID uuid.UUID, name string) bool {
	var token models.AccessToken
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND name = ?", accountID, name).
		First(&token))
	return token.RevokedAt != nil
}

func (s *accountTokensSuite) assertCreateForbidden(resp contractstesting.Response) {
	s.Equal(http.StatusForbidden, s.statusOf(resp))
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Data json.RawMessage `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("forbidden", parsed.Error.Code)
	s.Equal("forbidden", parsed.Error.Message)
	s.Empty(parsed.Data)
}

func (s *accountTokensSuite) body(response contractstesting.Response) string {
	s.T().Helper()
	content, err := response.Content()
	s.Require().NoError(err)
	return content
}

func (s *accountTokensSuite) statusOf(response contractstesting.Response) int {
	s.T().Helper()
	value := reflect.ValueOf(response)
	s.Require().Equal(reflect.Ptr, value.Kind())
	field := value.Elem().FieldByName("response")
	s.Require().True(field.IsValid())
	raw := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	httpResponse, ok := raw.Interface().(*http.Response)
	s.Require().True(ok)
	s.Require().NotNil(httpResponse)
	return httpResponse.StatusCode
}
