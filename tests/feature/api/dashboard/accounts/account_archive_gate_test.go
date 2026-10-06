package accounts

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
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const accountArchiveGatePassword = "correct-horse-battery"

// AccountArchiveGateTestSuite drives POST /v1/accounts/{accountId}/archive
// through Can(account.lifecycle). The owner may archive the account. Admin,
// auditor and user are refused before the status is written.
type AccountArchiveGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountArchiveGateSuite(t *testing.T) {
	suite.Run(t, new(AccountArchiveGateTestSuite))
}

func (s *AccountArchiveGateTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *AccountArchiveGateTestSuite) TestAccountLifecycleFollowsTheAccountRole() {
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Account " + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))

	for _, role := range []string{models.AccountRoleAdmin, models.AccountRoleAuditor, models.AccountRoleUser} {
		s.assertForbidden(s.archive(s.login(role, accountID), accountID))
		s.Equal(models.StatusActive, s.storedStatus(accountID))
	}

	resp := s.archive(s.login(models.AccountRoleOwner, accountID), accountID)
	resp.AssertOk()
	s.Equal(models.AccountStatusArchived, s.storedStatus(accountID))
}

func (s *AccountArchiveGateTestSuite) login(role string, accountID uuid.UUID) string {
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	hash, err := authsvc.NewService().HashPassword(accountArchiveGatePassword)
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

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountArchiveGatePassword)
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

func (s *AccountArchiveGateTestSuite) archive(token string, accountID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Post("/v1/accounts/"+accountID.String()+"/archive", nil)
	s.Require().NoError(err)
	return resp
}

func (s *AccountArchiveGateTestSuite) assertForbidden(resp contractstesting.Response) {
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

func (s *AccountArchiveGateTestSuite) storedStatus(accountID uuid.UUID) string {
	var account models.Account
	s.Require().NoError(facades.Orm().Query().Where("id = ?", accountID).First(&account))
	return account.Status
}
