package accounts

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const (
	accountTokenReadGatePassword = "correct-horse-battery"
	accountTokenReadGateName     = "listed-token"
	accountTokenReadGateHash     = "fixture-token-hash"
)

// AccountTokenReadGateTestSuite drives GET /v1/accounts/{accountId}/tokens
// through Can(tokens.read). Owner, admin, and auditor may list. The user
// role is refused before the handler returns the token list.
type AccountTokenReadGateTestSuite struct {
	support.HTTPSuite
}

func TestAccount_Token_ReadGateSuite(t *testing.T) {
	support.RunSuite(t, new(AccountTokenReadGateTestSuite))
}

func (s *AccountTokenReadGateTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *AccountTokenReadGateTestSuite) TestAccount_Token_ListFollowsTheAccountRole() {
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Account " + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccessToken{
		ID: uuid.New(), AccountID: accountID, Name: accountTokenReadGateName, TokenHash: accountTokenReadGateHash,
		Permissions: "[]", SpendingLimit: "{}",
	}))

	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleAuditor} {
		resp := s.listTokens(s.login(role, accountID), accountID)
		resp.AssertOk()
		content, err := resp.Content()
		s.Require().NoError(err)
		s.Contains(content, accountTokenReadGateName)
		s.NotContains(content, accountTokenReadGateHash)
	}

	s.Equal(int64(1), s.tokenCount(accountID))
	s.assertForbidden(s.listTokens(s.login(models.AccountRoleUser, accountID), accountID))
	s.Equal(int64(1), s.tokenCount(accountID))
}

func (s *AccountTokenReadGateTestSuite) login(role string, accountID uuid.UUID) string {
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	hash, err := authsvc.NewService().HashPassword(accountTokenReadGatePassword)
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

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountTokenReadGatePassword)
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

func (s *AccountTokenReadGateTestSuite) listTokens(token string, accountID uuid.UUID) contractstesting.Response {
	resp := s.Get("/v1/accounts/"+accountID.String()+"/tokens", support.Session{AccessToken: token})
	return resp
}

func (s *AccountTokenReadGateTestSuite) assertForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	content, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(content, accountTokenReadGateName)
	s.NotContains(content, accountTokenReadGateHash)
	s.AssertError(resp, 403, "forbidden", "forbidden")
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Data json.RawMessage `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Empty(parsed.Data)
}

func (s *AccountTokenReadGateTestSuite) tokenCount(accountID uuid.UUID) int64 {
	total, err := facades.Orm().Query().Model(&models.AccessToken{}).
		Where("account_id = ?", accountID).
		Count()
	s.Require().NoError(err)
	return total
}
