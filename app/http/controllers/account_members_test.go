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

const membersTestPassword = "correct-horse-battery"

type AccountMembersTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountMembersSuite(t *testing.T) {
	suite.Run(t, new(AccountMembersTestSuite))
}

func (s *AccountMembersTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

type memberSession struct {
	id    uuid.UUID
	token string
}

func (s *AccountMembersTestSuite) loginUser(role, status string, accountID uuid.UUID) memberSession {
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	hash, err := authsvc.NewService().HashPassword(membersTestPassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: status,
	}))
	return memberSession{id: userID, token: s.login(email)}
}

func (s *AccountMembersTestSuite) login(email string) string {
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, membersTestPassword)
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

func (s *AccountMembersTestSuite) createAccount() uuid.UUID {
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Members " + accountID.String()[:8], Status: "active", Environment: "prod",
	}))
	return accountID
}

func (s *AccountMembersTestSuite) insertToken(accountID, createdBy uuid.UUID, name string) uuid.UUID {
	tokenID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, created_by, name, token_hash, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		tokenID, accountID, createdBy, name, "hash-"+tokenID.String()[:8], "{}",
	)
	s.Require().NoError(err)
	return tokenID
}

func (s *AccountMembersTestSuite) tokenCount(accountID, createdBy uuid.UUID) int64 {
	total, err := facades.Orm().Query().Model(&models.AccessToken{}).
		Where("account_id = ? AND created_by = ?", accountID, createdBy).
		Count()
	s.Require().NoError(err)
	return total
}

func (s *AccountMembersTestSuite) patchMember(token string, accountID, userID uuid.UUID, body string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Patch("/v1/accounts/"+accountID.String()+"/users/"+userID.String(), strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *AccountMembersTestSuite) getUsers(token string, accountID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get("/v1/accounts/" + accountID.String() + "/users")
	s.Require().NoError(err)
	return resp
}

func (s *AccountMembersTestSuite) getAccount(token string, accountID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get("/v1/accounts/" + accountID.String())
	s.Require().NoError(err)
	return resp
}

func (s *AccountMembersTestSuite) assertForbidden(resp contractstesting.Response, message string) {
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

func (s *AccountMembersTestSuite) storedRole(accountID, userID uuid.UUID) (string, string) {
	var member models.AccountUser
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		First(&member))
	return member.Role, member.Status
}

func (s *AccountMembersTestSuite) TestUsersReadFollowsTheAccountRole() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", models.MembershipStatusActive, accountID)
	admin := s.loginUser("admin", models.MembershipStatusActive, accountID)
	auditor := s.loginUser("auditor", models.MembershipStatusActive, accountID)
	user := s.loginUser("user", models.MembershipStatusActive, accountID)

	s.getUsers(owner.token, accountID).AssertOk()
	s.getUsers(admin.token, accountID).AssertOk()
	s.getUsers(auditor.token, accountID).AssertOk()
	s.assertForbidden(s.getUsers(user.token, accountID), "forbidden")
}

func (s *AccountMembersTestSuite) TestMissingAccountIsNotFoundBeforeUsersRead() {
	accountID := s.createAccount()
	user := s.loginUser("user", models.MembershipStatusActive, accountID)

	resp := s.getUsers(user.token, uuid.New())
	resp.AssertNotFound()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"code":"not_found"`)
	s.NotContains(content, `"message":"forbidden"`)
}

func (s *AccountMembersTestSuite) TestAdminCannotGrantOwner() {
	accountID := s.createAccount()
	admin := s.loginUser("admin", models.MembershipStatusActive, accountID)
	member := s.loginUser("user", models.MembershipStatusActive, accountID)
	s.loginUser("owner", models.MembershipStatusActive, accountID)

	resp := s.patchMember(admin.token, accountID, member.id, `{"role":"owner"}`)
	s.assertForbidden(resp, "cannot grant a role above your own")

	role, status := s.storedRole(accountID, member.id)
	s.Equal("user", role)
	s.Equal(models.MembershipStatusActive, status)
}

func (s *AccountMembersTestSuite) TestAdminCanGrantAdmin() {
	accountID := s.createAccount()
	admin := s.loginUser("admin", models.MembershipStatusActive, accountID)
	member := s.loginUser("user", models.MembershipStatusActive, accountID)
	s.loginUser("owner", models.MembershipStatusActive, accountID)

	resp := s.patchMember(admin.token, accountID, member.id, `{"role":"admin"}`)
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Equal(member.id.String(), parsed.UserID)
	s.Equal("admin", parsed.Role)
	s.Equal(models.MembershipStatusActive, parsed.Status)
}

func (s *AccountMembersTestSuite) TestSuspend_RevokesTokensAndBlocksMembership() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", models.MembershipStatusActive, accountID)
	member := s.loginUser("user", models.MembershipStatusActive, accountID)
	s.insertToken(accountID, member.id, "member-token")
	s.insertToken(accountID, owner.id, "owner-token")

	resp := s.patchMember(owner.token, accountID, member.id, `{"status":"suspended"}`)
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"status":"suspended"`)
	s.Contains(content, `"role":"user"`)

	s.Equal(int64(0), s.tokenCount(accountID, member.id))
	s.Equal(int64(1), s.tokenCount(accountID, owner.id))

	s.getAccount(member.token, accountID).AssertForbidden()
	s.getAccount(owner.token, accountID).AssertOk()
}

func (s *AccountMembersTestSuite) TestLastOwnerCannotBeSuspended() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", models.MembershipStatusActive, accountID)

	resp := s.patchMember(owner.token, accountID, owner.id, `{"status":"suspended"}`)
	s.assertForbidden(resp, "cannot remove or suspend the last owner")
	s.getAccount(owner.token, accountID).AssertOk()

	role, status := s.storedRole(accountID, owner.id)
	s.Equal("owner", role)
	s.Equal(models.MembershipStatusActive, status)
}

func (s *AccountMembersTestSuite) TestSelfChangeIsForbidden() {
	accountID := s.createAccount()
	s.loginUser("owner", models.MembershipStatusActive, accountID)
	admin := s.loginUser("admin", models.MembershipStatusActive, accountID)

	resp := s.patchMember(admin.token, accountID, admin.id, `{"role":"user"}`)
	s.assertForbidden(resp, "cannot change your own membership")

	role, _ := s.storedRole(accountID, admin.id)
	s.Equal("admin", role)
}

func (s *AccountMembersTestSuite) TestAuditorPatchIsForbidden() {
	accountID := s.createAccount()
	s.loginUser("owner", models.MembershipStatusActive, accountID)
	auditor := s.loginUser("auditor", models.MembershipStatusActive, accountID)
	member := s.loginUser("user", models.MembershipStatusActive, accountID)

	resp := s.patchMember(auditor.token, accountID, member.id, `{"status":"suspended"}`)
	s.assertForbidden(resp, "only owners and admins may manage members")

	_, status := s.storedRole(accountID, member.id)
	s.Equal(models.MembershipStatusActive, status)
}

func (s *AccountMembersTestSuite) TestUnknownRoleOrStatusIs422() {
	accountID := s.createAccount()
	admin := s.loginUser("admin", models.MembershipStatusActive, accountID)
	member := s.loginUser("user", models.MembershipStatusActive, accountID)
	s.loginUser("owner", models.MembershipStatusActive, accountID)

	roleResp := s.patchMember(admin.token, accountID, member.id, `{"role":"viewer"}`)
	s.assertFieldError(roleResp, "role")

	statusResp := s.patchMember(admin.token, accountID, member.id, `{"status":"frozen"}`)
	s.assertFieldError(statusResp, "status")

	role, status := s.storedRole(accountID, member.id)
	s.Equal("user", role)
	s.Equal(models.MembershipStatusActive, status)
}

func (s *AccountMembersTestSuite) TestRemove_RevokesTokensCreatedByTheMember() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", models.MembershipStatusActive, accountID)
	member := s.loginUser("user", models.MembershipStatusActive, accountID)
	s.insertToken(accountID, member.id, "member-token")
	s.insertToken(accountID, owner.id, "owner-token")

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+owner.token).
		Delete("/v1/accounts/"+accountID.String()+"/users/"+member.id.String(), nil)
	s.Require().NoError(err)
	resp.AssertNoContent()

	s.Equal(int64(0), s.tokenCount(accountID, member.id))
	s.Equal(int64(1), s.tokenCount(accountID, owner.id))
	s.getAccount(member.token, accountID).AssertForbidden()
}

func (s *AccountMembersTestSuite) assertFieldError(resp contractstesting.Response, field string) {
	resp.AssertStatus(422)
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Errors map[string][]string `json:"errors"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Equal("validation_failed", parsed.Error.Code)
	s.Equal("validation failed", parsed.Error.Message)
	s.NotEmpty(parsed.Errors[field])
}
