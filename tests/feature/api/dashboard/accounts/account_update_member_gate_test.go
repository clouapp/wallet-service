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

const accountUpdateMemberGatePassword = "correct-horse-battery"

// AccountUpdateMemberGateTestSuite drives PATCH /v1/accounts/{accountId}/users/{userId}
// through AccountUpdateMember (users.write). Owner and admin may change a member,
// including the rank limits. Auditor and user are refused before the membership changes.
type AccountUpdateMemberGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountUpdateMemberGateSuite(t *testing.T) {
	suite.Run(t, new(AccountUpdateMemberGateTestSuite))
}

func (s *AccountUpdateMemberGateTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *AccountUpdateMemberGateTestSuite) TestAccountUpdateMemberFollowsUsersWrite() {
	accountID := s.createAccount()

	for _, role := range []string{models.AccountRoleAuditor, models.AccountRoleUser} {
		targetID := s.insertMember(accountID, models.AccountRoleUser, models.MembershipStatusActive)
		s.assertForbidden(
			s.patchMember(s.login(role, accountID), accountID, targetID, `{"status":"suspended"}`),
			"only owners and admins may manage members",
		)
		s.Equal(models.AccountRoleUser, s.storedRole(accountID, targetID))
		s.Equal(models.MembershipStatusActive, s.storedStatus(accountID, targetID))
	}

	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin} {
		targetID := s.insertMember(accountID, models.AccountRoleUser, models.MembershipStatusActive)
		resp := s.patchMember(s.login(role, accountID), accountID, targetID, `{"role":"auditor"}`)
		resp.AssertOk()
		s.Equal(models.AccountRoleAuditor, s.storedRole(accountID, targetID))
		s.Equal(models.MembershipStatusActive, s.storedStatus(accountID, targetID))
	}
}

func (s *AccountUpdateMemberGateTestSuite) TestRankLimitsStayOnTheChange() {
	accountID := s.createAccount()
	s.insertMember(accountID, models.AccountRoleOwner, models.MembershipStatusActive)
	ownerID := s.insertMember(accountID, models.AccountRoleOwner, models.MembershipStatusActive)
	adminToken := s.login(models.AccountRoleAdmin, accountID)
	memberID := s.insertMember(accountID, models.AccountRoleUser, models.MembershipStatusActive)

	grant := s.patchMember(adminToken, accountID, memberID, `{"role":"owner"}`)
	s.assertForbidden(grant, "cannot grant a role above your own")
	s.Equal(models.AccountRoleUser, s.storedRole(accountID, memberID))
	s.Equal(models.MembershipStatusActive, s.storedStatus(accountID, memberID))

	act := s.patchMember(adminToken, accountID, ownerID, `{"status":"suspended"}`)
	s.assertForbidden(act, "cannot change a member above your rank")
	s.Equal(models.AccountRoleOwner, s.storedRole(accountID, ownerID))
	s.Equal(models.MembershipStatusActive, s.storedStatus(accountID, ownerID))

	equal := s.patchMember(adminToken, accountID, memberID, `{"role":"admin"}`)
	equal.AssertOk()
	s.Equal(models.AccountRoleAdmin, s.storedRole(accountID, memberID))
}

func (s *AccountUpdateMemberGateTestSuite) createAccount() uuid.UUID {
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Account " + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	return accountID
}

func (s *AccountUpdateMemberGateTestSuite) login(role string, accountID uuid.UUID) string {
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	s.insertUser(userID, email)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountUpdateMemberGatePassword)
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

func (s *AccountUpdateMemberGateTestSuite) insertMember(accountID uuid.UUID, role, status string) uuid.UUID {
	userID := uuid.New()
	s.insertUser(userID, role+"-target-"+userID.String()[:8]+"@example.com")
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: status,
	}))
	return userID
}

func (s *AccountUpdateMemberGateTestSuite) insertUser(userID uuid.UUID, email string) {
	hash, err := authsvc.NewService().HashPassword(accountUpdateMemberGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
}

func (s *AccountUpdateMemberGateTestSuite) patchMember(token string, accountID, targetID uuid.UUID, body string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Patch("/v1/accounts/"+accountID.String()+"/users/"+targetID.String(), strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *AccountUpdateMemberGateTestSuite) assertForbidden(resp contractstesting.Response, message string) {
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
	s.Equal(message, parsed.Error.Message)
}

func (s *AccountUpdateMemberGateTestSuite) storedRole(accountID, userID uuid.UUID) string {
	role, _ := s.storedMembership(accountID, userID)
	return role
}

func (s *AccountUpdateMemberGateTestSuite) storedStatus(accountID, userID uuid.UUID) string {
	_, status := s.storedMembership(accountID, userID)
	return status
}

func (s *AccountUpdateMemberGateTestSuite) storedMembership(accountID, userID uuid.UUID) (string, string) {
	var member models.AccountUser
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		First(&member))
	return member.Role, member.Status
}
