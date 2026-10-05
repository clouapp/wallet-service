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

const accountAddMemberGatePassword = "correct-horse-battery"

// AccountAddMemberGateTestSuite drives POST /v1/accounts/{accountId}/users
// through Can(users.write). Owner and admin may add a member. Auditor and
// user are refused before a membership is written.
type AccountAddMemberGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountAddMemberGateSuite(t *testing.T) {
	suite.Run(t, new(AccountAddMemberGateTestSuite))
}

func (s *AccountAddMemberGateTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *AccountAddMemberGateTestSuite) TestAccountAddMemberFollowsTheAccountRole() {
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Account " + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))

	for _, role := range []string{models.AccountRoleAuditor, models.AccountRoleUser} {
		targetID := s.insertUser(role + "-target-" + uuid.New().String()[:8] + "@example.com")
		s.assertForbidden(s.addMember(s.login(role, accountID), accountID, targetID))
		s.Equal(int64(0), s.membershipCount(accountID, targetID))
	}

	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin} {
		targetID := s.insertUser(role + "-target-" + uuid.New().String()[:8] + "@example.com")
		resp := s.addMember(s.login(role, accountID), accountID, targetID)
		resp.AssertStatus(201)
		s.Equal(int64(1), s.membershipCount(accountID, targetID))
		s.Equal(models.AccountRoleUser, s.storedRole(accountID, targetID))
	}
}

func (s *AccountAddMemberGateTestSuite) TestAddMemberValidationStaysUnprocessable() {
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Account " + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	token := s.login(models.AccountRoleOwner, accountID)
	before := s.accountMembershipCount(accountID)

	resp := s.post(token, accountID, `{"email":"not-an-email","role":"user"}`)
	resp.AssertStatus(422)
	s.Equal(before, s.accountMembershipCount(accountID))
}

func (s *AccountAddMemberGateTestSuite) login(role string, accountID uuid.UUID) string {
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	s.insertUserWithID(userID, email)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountAddMemberGatePassword)
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

func (s *AccountAddMemberGateTestSuite) insertUser(email string) uuid.UUID {
	userID := uuid.New()
	s.insertUserWithID(userID, email)
	return userID
}

func (s *AccountAddMemberGateTestSuite) insertUserWithID(userID uuid.UUID, email string) {
	hash, err := authsvc.NewService().HashPassword(accountAddMemberGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
}

func (s *AccountAddMemberGateTestSuite) addMember(token string, accountID, targetID uuid.UUID) contractstesting.Response {
	var email string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT email FROM users WHERE id = ?`, targetID).Scan(&email))
	body := fmt.Sprintf(`{"email":%q,"role":%q}`, email, models.AccountRoleUser)
	return s.post(token, accountID, body)
}

func (s *AccountAddMemberGateTestSuite) post(token string, accountID uuid.UUID, body string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Post("/v1/accounts/"+accountID.String()+"/users", strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *AccountAddMemberGateTestSuite) assertForbidden(resp contractstesting.Response) {
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

func (s *AccountAddMemberGateTestSuite) membershipCount(accountID, userID uuid.UUID) int64 {
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_users WHERE account_id = ? AND user_id = ? AND deleted_at IS NULL`,
		accountID, userID,
	).Scan(&total))
	return total
}

func (s *AccountAddMemberGateTestSuite) accountMembershipCount(accountID uuid.UUID) int64 {
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_users WHERE account_id = ? AND deleted_at IS NULL`,
		accountID,
	).Scan(&total))
	return total
}

func (s *AccountAddMemberGateTestSuite) storedRole(accountID, userID uuid.UUID) string {
	var role string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT role FROM account_users WHERE account_id = ? AND user_id = ? AND deleted_at IS NULL`,
		accountID, userID,
	).Scan(&role))
	return role
}
