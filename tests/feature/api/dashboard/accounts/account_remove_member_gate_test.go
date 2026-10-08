package accounts

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const accountRemoveMemberGatePassword = "correct-horse-battery"

// AccountRemoveMemberGateTestSuite drives DELETE /v1/accounts/{accountId}/users/{userId}
// through Can(users.write). Owner and admin may remove a member. Auditor and
// user are refused before the membership is removed.
type AccountRemoveMemberGateTestSuite struct {
	support.HTTPSuite
}

func TestAccount_Remove_MemberGateSuite(t *testing.T) {
	support.RunSuite(t, new(AccountRemoveMemberGateTestSuite))
}

func (s *AccountRemoveMemberGateTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *AccountRemoveMemberGateTestSuite) TestAccount_Remove_MemberFollowsTheAccountRole() {
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Account " + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))

	for _, role := range []string{models.AccountRoleAuditor, models.AccountRoleUser} {
		targetID := s.insertMember(accountID, models.AccountRoleUser)
		s.Equal(int64(1), s.membershipCount(accountID, targetID))
		s.assertForbidden(s.removeMember(s.login(role, accountID), accountID, targetID))
		s.Equal(int64(1), s.membershipCount(accountID, targetID))
	}

	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin} {
		targetID := s.insertMember(accountID, models.AccountRoleUser)
		s.Equal(int64(1), s.membershipCount(accountID, targetID))
		resp := s.removeMember(s.login(role, accountID), accountID, targetID)
		resp.AssertStatus(204)
		s.Equal(int64(0), s.membershipCount(accountID, targetID))
	}
}

func (s *AccountRemoveMemberGateTestSuite) login(role string, accountID uuid.UUID) string {
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	s.insertUserWithID(userID, email)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountRemoveMemberGatePassword)
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

func (s *AccountRemoveMemberGateTestSuite) insertMember(accountID uuid.UUID, role string) uuid.UUID {
	userID := uuid.New()
	s.insertUserWithID(userID, role+"-target-"+userID.String()[:8]+"@example.com")
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
	return userID
}

func (s *AccountRemoveMemberGateTestSuite) insertUserWithID(userID uuid.UUID, email string) {
	hash, err := authsvc.NewService(appfacades.Hash()).HashPassword(accountRemoveMemberGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
}

func (s *AccountRemoveMemberGateTestSuite) removeMember(token string, accountID, targetID uuid.UUID) contractstesting.Response {
	resp := s.Delete("/v1/accounts/"+accountID.String()+"/users/"+targetID.String(), support.Session{AccessToken: token}, nil)
	return resp
}

func (s *AccountRemoveMemberGateTestSuite) assertForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	s.AssertError(resp, 403, "forbidden", "forbidden")
}

func (s *AccountRemoveMemberGateTestSuite) membershipCount(accountID, userID uuid.UUID) int64 {
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_users WHERE account_id = ? AND user_id = ? AND deleted_at IS NULL`,
		accountID, userID,
	).Scan(&total))
	return total
}
