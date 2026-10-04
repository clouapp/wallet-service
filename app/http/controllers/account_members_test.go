package controllers_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
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

func (s *AccountMembersTestSuite) getInvites(token string, accountID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get("/v1/accounts/" + accountID.String() + "/invites")
	s.Require().NoError(err)
	return resp
}

func (s *AccountMembersTestSuite) insertInvite(accountID, invitedBy uuid.UUID, email, role, tokenHash string) {
	s.insertOpenInvite(accountID, invitedBy, email, role, tokenHash)
}

func (s *AccountMembersTestSuite) insertOpenInvite(accountID, invitedBy uuid.UUID, email, role, tokenHash string) uuid.UUID {
	id := uuid.New()
	_, err := facades.Orm().Query().Exec(`
		INSERT INTO account_invites (id, account_id, email, role, token_hash, invited_by, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, NOW() + INTERVAL '72 hours', NOW(), NOW())`,
		id, accountID, email, role, tokenHash, invitedBy,
	)
	s.Require().NoError(err)
	return id
}

func (s *AccountMembersTestSuite) inviteTokenHash(id uuid.UUID) string {
	s.T().Helper()
	var hash string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT token_hash FROM account_invites WHERE id = ?`, id).Scan(&hash))
	return hash
}

func (s *AccountMembersTestSuite) inviteExpiresEpoch(id uuid.UUID) int64 {
	s.T().Helper()
	var epoch int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT EXTRACT(EPOCH FROM expires_at)::bigint FROM account_invites WHERE id = ?`, id,
	).Scan(&epoch))
	return epoch
}

func (s *AccountMembersTestSuite) postResend(token string, accountID, inviteID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Post("/v1/accounts/"+accountID.String()+"/invites/"+inviteID.String()+"/resend", strings.NewReader(""))
	s.Require().NoError(err)
	return resp
}

func (s *AccountMembersTestSuite) TestListInvitesFollowsUsersReadAndOmitsTheToken() {
	accountID := s.createAccount()
	otherID := s.createAccount()
	owner := s.loginUser("owner", models.MembershipStatusActive, accountID)
	admin := s.loginUser("admin", models.MembershipStatusActive, accountID)
	auditor := s.loginUser("auditor", models.MembershipStatusActive, accountID)
	user := s.loginUser("user", models.MembershipStatusActive, accountID)
	const storedDigest = "stored-digest"
	s.insertInvite(accountID, owner.id, "pending-auditor@example.com", models.AccountRoleAuditor, storedDigest)
	s.insertInvite(otherID, owner.id, "other-account@example.com", models.AccountRoleUser, storedDigest)

	for _, token := range []string{owner.token, admin.token, auditor.token} {
		resp := s.getInvites(token, accountID)
		resp.AssertOk()
		content, err := resp.Content()
		s.Require().NoError(err)
		s.Contains(content, `"email":"pending-auditor@example.com"`)
		s.Contains(content, `"role":"auditor"`)
		s.NotContains(content, "other-account@example.com")
		s.NotContains(content, storedDigest)
		s.NotContains(content, "token_hash")
		s.NotContains(content, "invite_link")
	}
	s.assertForbidden(s.getInvites(user.token, accountID), "forbidden")
}

func (s *AccountMembersTestSuite) TestMissingAccountIsNotFoundBeforeInviteList() {
	accountID := s.createAccount()
	user := s.loginUser("user", models.MembershipStatusActive, accountID)

	resp := s.getInvites(user.token, uuid.New())
	resp.AssertNotFound()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"code":"not_found"`)
	s.NotContains(content, `"message":"forbidden"`)
}

func (s *AccountMembersTestSuite) postInvite(token string, accountID uuid.UUID, body string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Post("/v1/accounts/"+accountID.String()+"/invites", strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *AccountMembersTestSuite) insertUser(email string) uuid.UUID {
	userID := uuid.New()
	hash, err := authsvc.NewService().HashPassword(membersTestPassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *AccountMembersTestSuite) countActivity(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *AccountMembersTestSuite) countRows(model any, query string, args ...any) int64 {
	total, err := facades.Orm().Query().Model(model).Where(query, args...).Count()
	s.Require().NoError(err)
	return total
}

func (s *AccountMembersTestSuite) assertInviteAccepted(resp contractstesting.Response, email, role string) (string, string) {
	resp.AssertStatus(202)
	content, err := resp.Content()
	s.Require().NoError(err)
	var body map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &body))
	s.Equal(email, body["email"])
	s.Equal(role, body["role"])
	for _, key := range []string{"token", "token_hash", "invite_link", "raw_token", "exists", "user_exists", "password", "password_hash"} {
		_, present := body[key]
		s.False(present, key)
	}
	lowered := strings.ToLower(content)
	s.NotContains(lowered, "token")
	s.NotContains(lowered, "invite_link")
	s.NotContains(lowered, "exists")
	id, _ := body["id"].(string)
	s.NotEmpty(id)
	return content, id
}

func objectKeys(content string) []string {
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &body); err != nil {
		return nil
	}
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (s *AccountMembersTestSuite) TestCreateInviteAcceptsNewAndExistingEmailsWithoutAToken() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", models.MembershipStatusActive, accountID)
	admin := s.loginUser("admin", models.MembershipStatusActive, accountID)
	const existingEmail = "invite-existing@example.com"
	const newEmail = "invite-new@example.com"
	existingID := s.insertUser(existingEmail)

	existingResp := s.postInvite(admin.token, accountID, `{"email":"invite-existing@example.com","role":"admin"}`)
	existingBody, existingInviteID := s.assertInviteAccepted(existingResp, existingEmail, "admin")
	newResp := s.postInvite(owner.token, accountID, `{"email":"invite-new@example.com","role":"user"}`)
	newBody, newInviteID := s.assertInviteAccepted(newResp, newEmail, "user")
	s.Equal(objectKeys(existingBody), objectKeys(newBody))

	s.Equal(int64(0), s.countRows(&models.AccountUser{}, "account_id = ? AND user_id = ?", accountID, existingID))
	s.Equal(int64(0), s.countRows(&models.User{}, "email = ?", newEmail))
	s.Equal(int64(1), s.countRows(&models.User{}, "email = ?", existingEmail))

	for _, inviteID := range []string{existingInviteID, newInviteID} {
		var invite models.AccountInvite
		s.Require().NoError(facades.Orm().Query().Where("id = ?", inviteID).First(&invite))
		s.NotEmpty(invite.TokenHash)
		s.NotContains(existingBody, invite.TokenHash)
		s.NotContains(newBody, invite.TokenHash)
		var activity models.AccountActivity
		s.Require().NoError(facades.Orm().Query().
			Where("account_id = ? AND action = ? AND target_id = ?", accountID, "member.invited", inviteID).
			First(&activity))
		s.Len(activity.Metadata, 1)
		s.Equal(invite.Role, activity.Metadata["role"])
		encoded, err := json.Marshal(activity.Metadata)
		s.Require().NoError(err)
		s.NotContains(string(encoded), invite.TokenHash)
		s.NotContains(string(encoded), "token")

		s.Equal(int64(1), s.countActivity(
			`SELECT count(*) FROM activity_log
			 WHERE subject_type = 'account_invite' AND subject_id = ?
			   AND event = 'member.invited' AND scope = ? AND causer_type = 'users'
			   AND properties::text NOT LIKE '%token_hash%'
			   AND properties::text NOT LIKE '%' || ? || '%'`,
			inviteID, "account:"+accountID.String(), invite.TokenHash,
		))
	}

	listed := s.getInvites(owner.token, accountID)
	listed.AssertOk()
	listedBody, err := listed.Content()
	s.Require().NoError(err)
	s.Contains(listedBody, existingEmail)
	s.Contains(listedBody, newEmail)
	s.NotContains(listedBody, "token")
	s.NotContains(listedBody, "invite_link")
}

func (s *AccountMembersTestSuite) TestCreateInviteRefusesAuditorAndUser() {
	accountID := s.createAccount()
	auditor := s.loginUser("auditor", models.MembershipStatusActive, accountID)
	user := s.loginUser("user", models.MembershipStatusActive, accountID)
	body := `{"email":"refused@example.com","role":"user"}`

	s.assertForbidden(s.postInvite(auditor.token, accountID, body), "forbidden")
	s.assertForbidden(s.postInvite(user.token, accountID, body), "forbidden")
	s.Equal(int64(0), s.countRows(&models.AccountInvite{}, "account_id = ? AND email = ?", accountID, "refused@example.com"))
}

func (s *AccountMembersTestSuite) TestCreateInviteRefusesARoleAboveTheCaller() {
	accountID := s.createAccount()
	admin := s.loginUser("admin", models.MembershipStatusActive, accountID)
	s.loginUser("owner", models.MembershipStatusActive, accountID)

	resp := s.postInvite(admin.token, accountID, `{"email":"would-be-owner@example.com","role":"owner"}`)
	s.assertForbidden(resp, "cannot grant a role above your own")
	s.Equal(int64(0), s.countRows(&models.AccountInvite{}, "account_id = ? AND email = ?", accountID, "would-be-owner@example.com"))
}

func (s *AccountMembersTestSuite) TestMissingAccountIsNotFoundBeforeInviteCreate() {
	accountID := s.createAccount()
	user := s.loginUser("user", models.MembershipStatusActive, accountID)

	resp := s.postInvite(user.token, uuid.New(), `{"email":"missing@example.com","role":"user"}`)
	resp.AssertNotFound()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"code":"not_found"`)
	s.NotContains(content, `"message":"forbidden"`)
}

func (s *AccountMembersTestSuite) TestCreateInviteRejectsInvalidEmailAndRole() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", models.MembershipStatusActive, accountID)

	s.assertFieldError(s.postInvite(owner.token, accountID, `{"email":"not-an-email","role":"user"}`), "email")
	s.assertFieldError(s.postInvite(owner.token, accountID, `{"email":"valid@example.com","role":"viewer"}`), "role")
	s.Equal(int64(0), s.countRows(&models.AccountInvite{}, "account_id = ?", accountID))
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

func (s *AccountMembersTestSuite) TestSuspend_KeepsMintedTokensAndBlocksMembership() {
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

	s.Equal(int64(1), s.tokenCount(accountID, member.id))
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

func (s *AccountMembersTestSuite) TestResendInviteRotatesTheTokenForUsersWrite() {
	accountID := s.createAccount()
	otherID := s.createAccount()
	owner := s.loginUser("owner", models.MembershipStatusActive, accountID)
	admin := s.loginUser("admin", models.MembershipStatusActive, accountID)
	auditor := s.loginUser("auditor", models.MembershipStatusActive, accountID)
	user := s.loginUser("user", models.MembershipStatusActive, accountID)
	const rawToken = "resend-plain-secret"
	storedHash := accountsvc.HashInviteToken(rawToken)
	inviteID := s.insertOpenInvite(accountID, owner.id, "held-auditor@example.com", models.AccountRoleAuditor, storedHash)
	const otherHash = "other-account-digest"
	otherInviteID := s.insertOpenInvite(otherID, owner.id, "other-held@example.com", models.AccountRoleUser, otherHash)
	expiresBefore := s.inviteExpiresEpoch(inviteID)

	s.assertForbidden(s.postResend(auditor.token, accountID, inviteID), "forbidden")
	s.assertForbidden(s.postResend(user.token, accountID, inviteID), "forbidden")
	s.Equal(storedHash, s.inviteTokenHash(inviteID))

	missingAccount := s.postResend(user.token, uuid.New(), inviteID)
	missingAccount.AssertNotFound()
	missingBody, err := missingAccount.Content()
	s.Require().NoError(err)
	s.Contains(missingBody, `"code":"not_found"`)
	s.NotContains(missingBody, `"message":"forbidden"`)

	invalidID, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+owner.token).
		Post("/v1/accounts/"+accountID.String()+"/invites/not-a-uuid/resend", strings.NewReader(""))
	s.Require().NoError(err)
	invalidID.AssertStatus(400)

	unknown := s.postResend(owner.token, accountID, uuid.New())
	unknown.AssertNotFound()
	unknownBody, err := unknown.Content()
	s.Require().NoError(err)
	s.Contains(unknownBody, "invite is invalid or expired")

	other := s.postResend(owner.token, accountID, otherInviteID)
	other.AssertNotFound()
	s.Equal(otherHash, s.inviteTokenHash(otherInviteID))

	resent := s.postResend(owner.token, accountID, inviteID)
	content, id := s.assertInviteAccepted(resent, "held-auditor@example.com", models.AccountRoleAuditor)
	s.Equal(inviteID.String(), id)
	s.NotContains(content, rawToken)
	s.NotContains(content, storedHash)
	rotated := s.inviteTokenHash(inviteID)
	s.NotEqual(storedHash, rotated)
	s.Equal(expiresBefore, s.inviteExpiresEpoch(inviteID))
	s.Equal(models.AccountRoleAuditor, s.storedInviteRole(inviteID))
	s.Equal(int64(0), s.countActivity(`SELECT count(*) FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, accountID))

	preview, err := s.Http(s.T()).Get("/v1/auth/invites/" + rawToken)
	s.Require().NoError(err)
	preview.AssertNotFound()

	again := s.postResend(admin.token, accountID, inviteID)
	s.assertInviteAccepted(again, "held-auditor@example.com", models.AccountRoleAuditor)
	s.NotEqual(rotated, s.inviteTokenHash(inviteID))
	s.Equal(expiresBefore, s.inviteExpiresEpoch(inviteID))

	_, err = facades.Orm().Query().Exec(`UPDATE account_invites SET accepted_at = NOW() WHERE id = ?`, inviteID)
	s.Require().NoError(err)
	acceptedHash := s.inviteTokenHash(inviteID)
	accepted := s.postResend(owner.token, accountID, inviteID)
	accepted.AssertNotFound()
	s.Equal(acceptedHash, s.inviteTokenHash(inviteID))
}

func (s *AccountMembersTestSuite) storedInviteRole(id uuid.UUID) string {
	s.T().Helper()
	var role string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT role FROM account_invites WHERE id = ?`, id).Scan(&role))
	return role
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
