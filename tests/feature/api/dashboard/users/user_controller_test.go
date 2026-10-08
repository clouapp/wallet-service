package users

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const (
	myAccountsPath         = "/v1/users/me/accounts"
	myAccountsTestPassword = "correct-horse-battery"
	manyAccountsCount      = 64
	defaultAccountsLimit   = 20
	maxAccountsLimit       = 100
)

type listedAccount struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Environment string    `json:"environment"`
	Role        string    `json:"role"`
}

type accountListBody struct {
	Data   []listedAccount `json:"data"`
	Total  int64           `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// UserControllerTestSuite exercises GET /v1/users/me/accounts with a real
// dashboard session obtained from POST /v1/auth/login.
type UserControllerTestSuite struct {
	support.HTTPSuite
	userID uuid.UUID
	token  string
}

func TestUser_Controller_Suite(t *testing.T) {
	support.RunSuite(t, new(UserControllerTestSuite))
}

func (s *UserControllerTestSuite) SetupTest() {
	fixtures.TestDB(s.T())

	hash, err := authsvc.NewService(appfacades.Hash()).HashPassword(myAccountsTestPassword)
	s.Require().NoError(err)
	s.userID = uuid.New()
	email := "accounts-" + s.userID.String()[:8] + "@example.com"
	// Raw SQL so the NOT NULL preferences column takes its '{}' default.
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		s.userID, email, hash, "active",
	)
	s.Require().NoError(err)
	s.token = s.login(email)
}

func (s *UserControllerTestSuite) login(email string) string {
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, myAccountsTestPassword)
	resp := s.Post("/v1/auth/login", support.Session{}, body)
	resp.AssertStatus(200)

	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Require().NotEmpty(parsed.AccessToken)
	return parsed.AccessToken
}

func (s *UserControllerTestSuite) seedAccounts(count int, environment string) []models.Account {
	accounts := make([]models.Account, 0, count)
	for i := 1; i <= count; i++ {
		accounts = append(accounts, s.seedAccount(fmt.Sprintf("Account %02d", i), environment, "owner"))
	}
	return accounts
}

func (s *UserControllerTestSuite) seedAccount(name, environment, role string) models.Account {
	acc := models.Account{
		ID:          uuid.New(),
		Name:        name,
		Status:      "active",
		Environment: environment,
	}
	s.Require().NoError(facades.Orm().Query().Create(&acc))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: acc.ID, UserID: s.userID, Role: role,
	}))
	return acc
}

func (s *UserControllerTestSuite) TestGet_Me_ListsGloballyActiveFeatureKeys() {
	body := s.getMe()
	s.Equal([]string{features.FlagDepositScanEnabled, features.FlagSweepEnabled, features.FlagWalletCreationEnabled, features.FlagWebhookDeliveryEnabled, features.FlagWithdrawalsEnabled}, body.Features)

	account := s.seedAccount("Flags", "prod", "owner")
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO features (account_id, "key", enabled, created_at, updated_at)
		 VALUES (?, ?, false, NOW(), NOW()), (?, ?, true, NOW(), NOW())`,
		account.ID, features.FlagWithdrawalsEnabled,
		account.ID, features.FlagWalletCreationEnabled,
	)
	s.Require().NoError(err)
	body = s.getMe()
	s.Equal([]string{features.FlagDepositScanEnabled, features.FlagSweepEnabled, features.FlagWalletCreationEnabled, features.FlagWebhookDeliveryEnabled, features.FlagWithdrawalsEnabled}, body.Features)

	_, err = facades.Orm().Query().Exec(
		`INSERT INTO global_features ("key", enabled, created_at, updated_at)
		 VALUES (?, false, NOW(), NOW()), (?, true, NOW(), NOW())`,
		features.FlagWithdrawalsEnabled, features.FlagUser2FARequired,
	)
	s.Require().NoError(err)
	body = s.getMe()
	s.Equal([]string{features.FlagDepositScanEnabled, features.FlagSweepEnabled, features.FlagUser2FARequired, features.FlagWalletCreationEnabled, features.FlagWebhookDeliveryEnabled}, body.Features)

	patch := s.Patch("/v1/users/me", support.Session{AccessToken: s.token}, `{"full_name":"Renamed User"}`)
	patch.AssertOk()
	content, err := patch.Content()
	s.Require().NoError(err)
	s.NotContains(content, `"features"`)
}

func (s *UserControllerTestSuite) getMe() struct {
	Features []string `json:"features"`
} {
	s.T().Helper()
	resp := s.Get("/v1/users/me", support.Session{AccessToken: s.token})
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var body struct {
		Features []string `json:"features"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &body))
	return body
}

func (s *UserControllerTestSuite) TestUpdate_Me_AppliesFullName() {
	body := `{"full_name":"Renamed User"}`
	resp := s.Patch("/v1/users/me", support.Session{AccessToken: s.token}, body)
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		FullName string `json:"full_name"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Equal("Renamed User", parsed.FullName)
}

func (s *UserControllerTestSuite) TestUpdate_Account_AppliesName() {
	account := s.seedAccounts(1, "prod")[0]
	body := `{"name":"Renamed Account"}`
	resp := s.Patch("/v1/accounts/"+account.ID.String(), support.Session{AccessToken: s.token}, body)
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		Name string `json:"name"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Equal("Renamed Account", parsed.Name)
}

func (s *UserControllerTestSuite) listAccounts(query url.Values) contractstesting.Response {
	path := myAccountsPath
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	resp := s.Get(path, support.Session{AccessToken: s.token})
	return resp
}

func (s *UserControllerTestSuite) decodeList(resp contractstesting.Response) accountListBody {
	resp.AssertStatus(200)
	content, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(content, `"data":null`)
	var body accountListBody
	s.Require().NoError(json.Unmarshal([]byte(content), &body))
	return body
}

func (s *UserControllerTestSuite) TestList_MyAccounts_Unauthenticated() {
	resp := s.Get(myAccountsPath, support.Session{})
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

func (s *UserControllerTestSuite) TestList_MyAccounts_EmptyList() {
	body := s.decodeList(s.listAccounts(nil))

	s.Empty(body.Data)
	s.Equal(int64(0), body.Total)
	s.Equal(defaultAccountsLimit, body.Limit)
	s.Equal(0, body.Offset)
}

func (s *UserControllerTestSuite) TestList_MyAccounts_SinglePage() {
	s.seedAccounts(3, models.EnvironmentProd)

	body := s.decodeList(s.listAccounts(nil))

	s.Len(body.Data, 3)
	s.Equal(int64(3), body.Total)
	for _, account := range body.Data {
		s.Equal("owner", account.Role)
		s.NotEmpty(account.Name)
		s.Equal("active", account.Status)
	}
}

func (s *UserControllerTestSuite) TestList_MyAccounts_IncludesCallerRole() {
	owner := s.seedAccount("Owner Desk", models.EnvironmentProd, "owner")
	auditor := s.seedAccount("Audit Desk", models.EnvironmentProd, "auditor")

	resp := s.listAccounts(nil)
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"role":"owner"`)
	s.Contains(content, `"role":"auditor"`)
	s.Contains(content, `"name":"Owner Desk"`)
	s.Contains(content, `"view_all_wallets"`)

	body := s.decodeList(resp)
	roles := map[uuid.UUID]string{}
	for _, account := range body.Data {
		roles[account.ID] = account.Role
	}
	s.Equal("owner", roles[owner.ID])
	s.Equal("auditor", roles[auditor.ID])
}

func (s *UserControllerTestSuite) TestList_MyAccounts_ManyPagesReachEveryAccount() {
	seeded := s.seedAccounts(manyAccountsCount, models.EnvironmentProd)

	first := s.decodeList(s.listAccounts(nil))
	s.Require().Len(first.Data, defaultAccountsLimit)
	s.Equal(int64(manyAccountsCount), first.Total)
	s.Equal("Account 01", first.Data[0].Name)

	seen := map[uuid.UUID]bool{}
	for offset := 0; offset < manyAccountsCount; offset += defaultAccountsLimit {
		page := s.decodeList(s.listAccounts(url.Values{"offset": {fmt.Sprint(offset)}}))
		s.Equal(offset, page.Offset)
		for _, acc := range page.Data {
			s.False(seen[acc.ID], "account %s returned on more than one page", acc.Name)
			seen[acc.ID] = true
		}
	}
	s.Len(seen, len(seeded))
}

func (s *UserControllerTestSuite) TestList_MyAccounts_LastPageIsPartial() {
	s.seedAccounts(manyAccountsCount, models.EnvironmentProd)

	body := s.decodeList(s.listAccounts(url.Values{"limit": {"20"}, "offset": {"60"}}))

	s.Require().Len(body.Data, manyAccountsCount-60)
	s.Equal("Account 61", body.Data[0].Name)
	s.Equal("Account 64", body.Data[len(body.Data)-1].Name)
}

func (s *UserControllerTestSuite) TestList_MyAccounts_CapsLimitAtMax() {
	s.seedAccounts(manyAccountsCount, models.EnvironmentProd)

	body := s.decodeList(s.listAccounts(url.Values{"limit": {"500"}}))

	s.Equal(maxAccountsLimit, body.Limit)
	s.Len(body.Data, manyAccountsCount)
}

func (s *UserControllerTestSuite) TestList_MyAccounts_OutOfRangeOffsetReturnsEmptyPage() {
	s.seedAccounts(3, models.EnvironmentProd)

	body := s.decodeList(s.listAccounts(url.Values{"offset": {"300"}}))

	s.Empty(body.Data)
	s.Equal(int64(3), body.Total)
	s.Equal(300, body.Offset)
}

func (s *UserControllerTestSuite) TestList_MyAccounts_RejectsInvalidQuery() {
	testCases := []struct {
		name    string
		query   url.Values
		message string
	}{
		{name: "zero limit", query: url.Values{"limit": {"0"}}, message: "limit must be a positive integer"},
		{name: "negative limit", query: url.Values{"limit": {"-1"}}, message: "limit must be a positive integer"},
		{name: "non numeric limit", query: url.Values{"limit": {"all"}}, message: "limit must be a positive integer"},
		{name: "negative offset", query: url.Values{"offset": {"-20"}}, message: "offset must be a non-negative integer"},
		{name: "non numeric offset", query: url.Values{"offset": {"x"}}, message: "offset must be a non-negative integer"},
		{name: "unknown environment", query: url.Values{"environment": {"staging"}}, message: `environment must be "prod" or "test"`},
		{name: "search too long", query: url.Values{"search": {strings.Repeat("a", 101)}}, message: "search must be at most 100 characters"},
	}

	for _, testCase := range testCases {
		s.Run(testCase.name, func() {
			resp := s.listAccounts(testCase.query)
			s.AssertError(resp, 400, "invalid_request", testCase.message)
		})
	}
}

func (s *UserControllerTestSuite) TestList_MyAccounts_SearchAndEnvironmentFilter() {
	s.seedAccounts(manyAccountsCount, models.EnvironmentProd)
	s.seedAccounts(1, models.EnvironmentTest)

	searched := s.decodeList(s.listAccounts(url.Values{"search": {" account 4 "}, "environment": {models.EnvironmentProd}}))
	s.Equal(int64(10), searched.Total)
	s.Equal("Account 40", searched.Data[0].Name)

	testOnly := s.decodeList(s.listAccounts(url.Values{"environment": {models.EnvironmentTest}}))
	s.Equal(int64(1), testOnly.Total)
	s.Equal(models.EnvironmentTest, testOnly.Data[0].Environment)

	all := s.decodeList(s.listAccounts(nil))
	s.Equal(int64(manyAccountsCount+1), all.Total)
}
