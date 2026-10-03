package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/mocks"
)

const accountTokenPassword = "correct-horse-battery"

// accountTokensSuite checks S3.4.6's mint rule: permissions is a JSON array
// from the API catalog, and the creator cannot grant a permission the role
// does not hold. The suite never prints the minted token.
type accountTokensSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountTokenPermissions(t *testing.T) {
	suite.Run(t, new(accountTokensSuite))
}

func (s *accountTokensSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *accountTokensSuite) TestOwnerMintsACatalogSubset() {
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
	s.Equal(`["wallets.read","webhooks.write"]`, s.storedPermissions(accountID, "ci"))
}

func (s *accountTokensSuite) TestOmittedPermissionsStayEmpty() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"plain"}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.False(s.metadataHasPermissions(resp))
	s.Equal("", s.storedPermissions(accountID, "plain"))
}

func (s *accountTokensSuite) TestEmptyPermissionListStaysEmpty() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"empty","permissions":[]}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.False(s.metadataHasPermissions(resp))
	s.Equal("", s.storedPermissions(accountID, "empty"))
}

func (s *accountTokensSuite) TestNameOutsideTheCatalogIsRejected() {
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

func (s *accountTokensSuite) TestBlankSpendingLimitStaysEmptyObject() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"blank-cap","spending_limit":{}}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.Equal("{}", s.storedSpendingLimit(accountID, "blank-cap"))
}

func (s *accountTokensSuite) TestDailyUSDSpendingLimitIsStored() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"capped","spending_limit":{"daily_usd":"12.50"}}`)
	s.Equal(http.StatusCreated, s.statusOf(resp))
	s.Equal(`{"daily_usd":"12.50"}`, s.storedSpendingLimit(accountID, "capped"))
}

func (s *accountTokensSuite) TestNegativeSpendingLimitIsNotStored() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)

	resp := s.createToken(owner.token, accountID, `{"name":"negative","spending_limit":{"daily_usd":"-1"}}`)
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
	s.NotEmpty(parsed.Errors["spending_limit.daily_usd"])
}

func (s *accountTokensSuite) TestAuditorCanListAndCannotMint() {
	accountID := s.createAccount()
	s.loginUser("owner", accountID)
	auditor := s.loginUser("auditor", accountID)

	list, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+auditor.token).
		Get("/v1/accounts/" + accountID.String() + "/tokens")
	s.Require().NoError(err)
	s.Equal(http.StatusOK, s.statusOf(list))

	resp := s.createToken(auditor.token, accountID, `{"name":"nope"}`)
	s.Equal(http.StatusForbidden, s.statusOf(resp))
	s.Contains(s.body(resp), "only owners and admins may manage tokens")
	s.Equal(int64(0), s.tokenCount(accountID))
}

func (s *accountTokensSuite) TestUserCannotListTokens() {
	accountID := s.createAccount()
	s.loginUser("owner", accountID)
	user := s.loginUser("user", accountID)

	list, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+user.token).
		Get("/v1/accounts/" + accountID.String() + "/tokens")
	s.Require().NoError(err)
	s.Equal(http.StatusForbidden, s.statusOf(list))
	s.Contains(s.body(list), "only owners, admins, and auditors may read tokens")
}

func (s *accountTokensSuite) TestUserCannotMint() {
	accountID := s.createAccount()
	s.loginUser("owner", accountID)
	user := s.loginUser("user", accountID)

	resp := s.createToken(user.token, accountID, `{"name":"nope","permissions":["wallets.create"]}`)
	s.Equal(http.StatusForbidden, s.statusOf(resp))
	s.Contains(s.body(resp), "only owners and admins may manage tokens")
	s.Equal(int64(0), s.tokenCount(accountID))
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
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
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
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Post("/v1/accounts/"+accountID.String()+"/tokens", strings.NewReader(body))
	s.Require().NoError(err)
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

func (s *accountTokensSuite) storedSpendingLimit(accountID uuid.UUID, name string) string {
	var token models.AccessToken
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND name = ?", accountID, name).
		First(&token))
	return token.SpendingLimit
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
