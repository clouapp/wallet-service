package controllers_test

import (
	"encoding/json"
	"fmt"
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

const accountUpdateGatePassword = "correct-horse-battery"

// AccountUpdateGateTestSuite drives PATCH /v1/accounts/{accountId} through
// Can(account.write). Owner and admin may rename the account. Auditor and
// user are refused before the name is written.
type AccountUpdateGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountUpdateGateSuite(t *testing.T) {
	suite.Run(t, new(AccountUpdateGateTestSuite))
}

func (s *AccountUpdateGateTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *AccountUpdateGateTestSuite) TestAccountWriteFollowsTheAccountRole() {
	accountID := uuid.New()
	original := "Account " + accountID.String()[:8]
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: original, Status: "active", Environment: "prod",
	}))

	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin} {
		name := role + "-renamed"
		resp := s.patch(s.login(role, accountID), accountID, name)
		resp.AssertOk()
		s.Equal(name, s.storedName(accountID))
	}

	for _, role := range []string{models.AccountRoleAuditor, models.AccountRoleUser} {
		before := s.storedName(accountID)
		s.assertForbidden(s.patch(s.login(role, accountID), accountID, role+"-renamed"))
		s.Equal(before, s.storedName(accountID))
	}
}

func (s *AccountUpdateGateTestSuite) login(role string, accountID uuid.UUID) string {
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	hash, err := authsvc.NewService().HashPassword(accountUpdateGatePassword)
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

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountUpdateGatePassword)
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

func (s *AccountUpdateGateTestSuite) patch(token string, accountID uuid.UUID, name string) contractstesting.Response {
	body := fmt.Sprintf(`{"name":%q}`, name)
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Patch("/v1/accounts/"+accountID.String(), strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *AccountUpdateGateTestSuite) assertForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Equal("forbidden", parsed.Error.Code)
	s.Equal("forbidden", parsed.Error.Message)
}

func (s *AccountUpdateGateTestSuite) storedName(accountID uuid.UUID) string {
	var account models.Account
	s.Require().NoError(facades.Orm().Query().Where("id = ?", accountID).First(&account))
	return account.Name
}
