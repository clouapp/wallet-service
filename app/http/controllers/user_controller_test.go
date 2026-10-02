package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	myAccountsPath         = "/v1/users/me/accounts"
	myAccountsTestPassword = "correct-horse-battery"
	manyAccountsCount      = 64
	defaultAccountsLimit   = 20
	maxAccountsLimit       = 100
)

type accountListBody struct {
	Data   []models.Account `json:"data"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// UserControllerTestSuite exercises GET /v1/users/me/accounts with a real
// dashboard session obtained from POST /v1/auth/login.
type UserControllerTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
	userID uuid.UUID
	token  string
}

func TestUserControllerSuite(t *testing.T) {
	suite.Run(t, new(UserControllerTestSuite))
}

func (s *UserControllerTestSuite) SetupTest() {
	mocks.TestDB(s.T())

	hash, err := authsvc.NewService().HashPassword(myAccountsTestPassword)
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
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
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
		acc := models.Account{
			ID:          uuid.New(),
			Name:        fmt.Sprintf("Account %02d", i),
			Status:      "active",
			Environment: environment,
		}
		s.Require().NoError(facades.Orm().Query().Create(&acc))
		s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
			ID: uuid.New(), AccountID: acc.ID, UserID: s.userID, Role: "owner",
		}))
		accounts = append(accounts, acc)
	}
	return accounts
}

func (s *UserControllerTestSuite) listAccounts(query url.Values) contractstesting.Response {
	path := myAccountsPath
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+s.token).
		Get(path)
	s.Require().NoError(err)
	return resp
}

func (s *UserControllerTestSuite) decodeList(resp contractstesting.Response) accountListBody {
	resp.AssertStatus(200)
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Require().NotContains(content, `"data":null`)
	var body accountListBody
	s.Require().NoError(json.Unmarshal([]byte(content), &body))
	return body
}

func (s *UserControllerTestSuite) TestListMyAccounts_Unauthenticated() {
	resp, err := s.Http(s.T()).Get(myAccountsPath)
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

func (s *UserControllerTestSuite) TestListMyAccounts_EmptyList() {
	body := s.decodeList(s.listAccounts(nil))

	s.Empty(body.Data)
	s.Equal(int64(0), body.Total)
	s.Equal(defaultAccountsLimit, body.Limit)
	s.Equal(0, body.Offset)
}

func (s *UserControllerTestSuite) TestListMyAccounts_SinglePage() {
	s.seedAccounts(3, models.EnvironmentProd)

	body := s.decodeList(s.listAccounts(nil))

	s.Len(body.Data, 3)
	s.Equal(int64(3), body.Total)
}

func (s *UserControllerTestSuite) TestListMyAccounts_ManyPagesReachEveryAccount() {
	seeded := s.seedAccounts(manyAccountsCount, models.EnvironmentProd)

	first := s.decodeList(s.listAccounts(nil))
	s.Len(first.Data, defaultAccountsLimit)
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

func (s *UserControllerTestSuite) TestListMyAccounts_LastPageIsPartial() {
	s.seedAccounts(manyAccountsCount, models.EnvironmentProd)

	body := s.decodeList(s.listAccounts(url.Values{"limit": {"20"}, "offset": {"60"}}))

	s.Len(body.Data, manyAccountsCount-60)
	s.Equal("Account 61", body.Data[0].Name)
	s.Equal("Account 64", body.Data[len(body.Data)-1].Name)
}

func (s *UserControllerTestSuite) TestListMyAccounts_CapsLimitAtMax() {
	s.seedAccounts(manyAccountsCount, models.EnvironmentProd)

	body := s.decodeList(s.listAccounts(url.Values{"limit": {"500"}}))

	s.Equal(maxAccountsLimit, body.Limit)
	s.Len(body.Data, manyAccountsCount)
}

func (s *UserControllerTestSuite) TestListMyAccounts_OutOfRangeOffsetReturnsEmptyPage() {
	s.seedAccounts(3, models.EnvironmentProd)

	body := s.decodeList(s.listAccounts(url.Values{"offset": {"300"}}))

	s.Empty(body.Data)
	s.Equal(int64(3), body.Total)
	s.Equal(300, body.Offset)
}

func (s *UserControllerTestSuite) TestListMyAccounts_RejectsInvalidQuery() {
	testCases := []struct {
		name  string
		query url.Values
	}{
		{name: "zero limit", query: url.Values{"limit": {"0"}}},
		{name: "negative limit", query: url.Values{"limit": {"-1"}}},
		{name: "non numeric limit", query: url.Values{"limit": {"all"}}},
		{name: "negative offset", query: url.Values{"offset": {"-20"}}},
		{name: "non numeric offset", query: url.Values{"offset": {"x"}}},
		{name: "unknown environment", query: url.Values{"environment": {"staging"}}},
		{name: "search too long", query: url.Values{"search": {strings.Repeat("a", 101)}}},
	}

	for _, testCase := range testCases {
		s.Run(testCase.name, func() {
			resp := s.listAccounts(testCase.query)
			resp.AssertStatus(400)
			content, err := resp.Content()
			s.Require().NoError(err)
			s.Contains(content, `"error"`)
		})
	}
}

func (s *UserControllerTestSuite) TestListMyAccounts_SearchAndEnvironmentFilter() {
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
