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
	activitylog "github.com/macrowallets/waas/app/services/activity"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	activityTestPassword = "correct-horse-battery"
	activityTokenHash    = "activity-token-hash-do-not-store"
	activityPlainSecret  = "activity-signing-secret-do-not-store"
)

type AccountActivityTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountActivitySuite(t *testing.T) {
	suite.Run(t, new(AccountActivityTestSuite))
}

func (s *AccountActivityTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

type activitySession struct {
	id    uuid.UUID
	token string
}

type activityPage struct {
	Data []struct {
		AccountID   string `json:"account_id"`
		ActorUserID string `json:"actor_user_id"`
		Action      string `json:"action"`
		TargetType  string `json:"target_type"`
		TargetID    string `json:"target_id"`
		Metadata    struct {
			Role    string   `json:"role"`
			Status  string   `json:"status"`
			Group   string   `json:"group"`
			Fields  []string `json:"fields"`
			Key     string   `json:"key"`
			Enabled *bool    `json:"enabled"`
		} `json:"metadata"`
	} `json:"data"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

func (s *AccountActivityTestSuite) TestRoleChangeCommitsMembershipAndActivityWithoutSecrets() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)
	member := s.loginUser("user", accountID)
	auditor := s.loginUser("auditor", accountID)
	s.insertToken(accountID, member.id)

	resp := s.patchMember(owner.token, accountID, member.id, `{"role":"admin"}`)
	resp.AssertOk()

	role, status := s.storedRole(accountID, member.id)
	s.Equal("admin", role)
	s.Equal(models.MembershipStatusActive, status)

	page := s.list(auditor.token, accountID, "")
	s.Equal(int64(1), page.Total)
	s.Require().Len(page.Data, 1)
	row := page.Data[0]
	s.Equal("member.role_changed", row.Action)
	s.Equal("account_user", row.TargetType)
	s.Equal(member.id.String(), row.TargetID)
	s.Equal(owner.id.String(), row.ActorUserID)
	s.Equal(accountID.String(), row.AccountID)
	s.Equal("admin", row.Metadata.Role)
	s.Empty(row.Metadata.Status)
	s.NotContains(s.pageText(page), activityTokenHash)
	s.NotContains(s.pageText(page), activityTestPassword)
}

func (s *AccountActivityTestSuite) TestSettingsSecretPatchDoesNotStoreTheSecret() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)
	auditor := s.loginUser("auditor", accountID)

	s.patch(owner.token, "/v1/accounts/"+accountID.String()+"/settings/account_webhooks",
		fmt.Sprintf(`{"signing_secret":%q}`, activityPlainSecret), 200)

	stored := s.storedSecret(accountID)
	s.NotEqual(activityPlainSecret, stored)
	s.NotEmpty(stored)

	page := s.list(auditor.token, accountID, "")
	s.Require().Len(page.Data, 1)
	row := page.Data[0]
	s.Equal("settings.updated", row.Action)
	s.Equal("settings", row.TargetType)
	s.Equal("account_webhooks", row.TargetID)
	s.Equal("account_webhooks", row.Metadata.Group)
	s.Equal([]string{"signing_secret"}, row.Metadata.Fields)
	text := s.pageText(page)
	s.NotContains(text, activityPlainSecret)
	s.NotContains(text, stored)
	s.NotContains(text, "enc:v1:")
}

func (s *AccountActivityTestSuite) TestAuditorListsNewestFirstAndUserIsForbidden() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)
	member := s.loginUser("user", accountID)
	auditor := s.loginUser("auditor", accountID)
	user := s.loginUser("user", accountID)

	s.patchMember(owner.token, accountID, member.id, `{"role":"auditor"}`).AssertOk()
	s.patch(owner.token, "/v1/accounts/"+accountID.String()+"/features/"+features.FlagWithdrawalsEnabled,
		`{"enabled":false}`, 200)
	s.patch(owner.token, "/v1/accounts/"+accountID.String()+"/settings/account_webhooks",
		fmt.Sprintf(`{"signing_secret":%q}`, activityPlainSecret), 200)

	role, _ := s.storedRole(accountID, member.id)
	s.Equal("auditor", role)

	newest := s.list(auditor.token, accountID, "limit=1")
	s.Equal(int64(3), newest.Total)
	s.Equal(1, newest.Limit)
	s.Equal(0, newest.Offset)
	s.Require().Len(newest.Data, 1)
	s.Equal("settings.updated", newest.Data[0].Action)

	middle := s.list(auditor.token, accountID, "limit=1&offset=1")
	s.Require().Len(middle.Data, 1)
	s.Equal("features.updated", middle.Data[0].Action)
	s.Equal(features.FlagWithdrawalsEnabled, middle.Data[0].Metadata.Key)
	s.Require().NotNil(middle.Data[0].Metadata.Enabled)
	s.False(*middle.Data[0].Metadata.Enabled)

	oldest := s.list(auditor.token, accountID, "limit=1&offset=2")
	s.Require().Len(oldest.Data, 1)
	s.Equal("member.role_changed", oldest.Data[0].Action)
	s.Equal("auditor", oldest.Data[0].Metadata.Role)

	forbidden := s.get(user.token, "/v1/accounts/"+accountID.String()+"/activity")
	forbidden.AssertForbidden()
	content, err := forbidden.Content()
	s.Require().NoError(err)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &body))
	s.Equal("forbidden", body.Error.Code)
	s.Equal("you do not have permission to view account activity", body.Error.Message)
}

func (s *AccountActivityTestSuite) TestPlatformFeatureWriteUsesANullAccount() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)
	s.grantPlatformAdmin(owner.id)

	s.patch(owner.token, "/v1/platform/features/"+features.FlagSweepEnabled, `{"enabled":false}`, 200)

	page := s.list(owner.token, accountID, "")
	s.Equal(int64(0), page.Total)
	s.Empty(page.Data)

	var stored struct {
		AccountID *string         `json:"account_id"`
		Metadata  json.RawMessage `json:"metadata"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT account_id::text AS account_id, metadata FROM account_activity WHERE actor_user_id = ?`,
		owner.id,
	).Scan(&stored)
	s.Require().NoError(err)
	s.Nil(stored.AccountID)
	var audit struct {
		Before map[string]bool `json:"before"`
		After  map[string]bool `json:"after"`
		Key    string          `json:"key"`
	}
	s.Require().NoError(json.Unmarshal(stored.Metadata, &audit))
	s.True(audit.Before[features.FlagSweepEnabled])
	s.False(audit.After[features.FlagSweepEnabled])
	s.Empty(audit.Key)
	s.NotContains(string(stored.Metadata), activityPlainSecret)
	s.Equal(int64(0), s.countActivity(activitylog.ActionFeaturesUpdated))
	s.Equal(int64(0), s.countActivity(activitylog.ActionUserFeaturesUpdated))
	s.Equal(int64(0), s.countActivity(activitylog.ActionChainFeaturesUpdated))

	platform := s.listPlatform(owner.token, "")
	s.Equal(int64(1), platform.Total)
	s.Require().Len(platform.Data, 1)
	s.Equal(activitylog.ActionFeaturesGlobalUpdated, platform.Data[0].Action)
	s.Empty(platform.Data[0].AccountID)

	outsider := s.loginUser("owner", accountID)
	denied := s.get(outsider.token, "/v1/platform/activity")
	denied.AssertForbidden()
}

func (s *AccountActivityTestSuite) TestMemberRemovedStaysOnTheAccountTrail() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)
	member := s.loginUser("user", accountID)

	resp := s.delete(owner.token, "/v1/accounts/"+accountID.String()+"/users/"+member.id.String())
	resp.AssertNoContent()

	page := s.list(owner.token, accountID, "")
	s.Equal(int64(1), page.Total)
	s.Require().Len(page.Data, 1)
	s.Equal("member.removed", page.Data[0].Action)
	s.Equal("user", page.Data[0].Metadata.Role)
	s.Equal(member.id.String(), page.Data[0].TargetID)
	s.NotContains(fmt.Sprint(page.Data[0].Metadata), activityTokenHash)
}

func (s *AccountActivityTestSuite) TestMFAResetIsAPlatformRow() {
	accountID := s.createAccount()
	owner := s.loginUser("owner", accountID)
	s.grantPlatformAdmin(owner.id)

	resp := s.delete(owner.token, "/v1/users/me/totp")
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var renewed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &renewed))
	s.Require().NotEmpty(renewed.AccessToken)
	owner.token = renewed.AccessToken

	accountPage := s.list(owner.token, accountID, "")
	s.Equal(int64(0), accountPage.Total)

	platform := s.listPlatform(owner.token, "")
	s.Equal(int64(2), platform.Total)
	s.Require().Len(platform.Data, 2)
	seen := map[string]bool{}
	for _, row := range platform.Data {
		s.Empty(row.AccountID)
		seen[row.Action] = true
		switch row.Action {
		case "user.mfa_reset":
			s.Equal("totp", row.Metadata.Key)
			s.Require().NotNil(row.Metadata.Enabled)
			s.False(*row.Metadata.Enabled)
		case "user.sessions_revoked":
			s.Equal("sessions", row.Metadata.Key)
			s.Equal("user", row.TargetType)
			s.Equal(owner.id.String(), row.TargetID)
		default:
			s.Fail("unexpected platform action", row.Action)
		}
	}
	s.True(seen["user.mfa_reset"])
	s.True(seen["user.sessions_revoked"])
	encoded, err := json.Marshal(platform.Data)
	s.Require().NoError(err)
	s.NotContains(string(encoded), "totp_secret")
	s.NotContains(string(encoded), activityPlainSecret)
	s.NotContains(string(encoded), "token_hash")
}

func (s *AccountActivityTestSuite) loginUser(role string, accountID uuid.UUID) activitySession {
	s.T().Helper()
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	hash, err := authsvc.NewService().HashPassword(activityTestPassword)
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
	return activitySession{id: userID, token: s.login(email)}
}

func (s *AccountActivityTestSuite) login(email string) string {
	s.T().Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, activityTestPassword)
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

func (s *AccountActivityTestSuite) createAccount() uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Activity " + accountID.String()[:8], Status: "active", Environment: "prod",
	}))
	return accountID
}

func (s *AccountActivityTestSuite) insertToken(accountID, createdBy uuid.UUID) {
	s.T().Helper()
	tokenID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, created_by, name, token_hash, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		tokenID, accountID, createdBy, "member-token", activityTokenHash, "{}",
	)
	s.Require().NoError(err)
}

func (s *AccountActivityTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *AccountActivityTestSuite) patchMember(token string, accountID, userID uuid.UUID, body string) contractstesting.Response {
	s.T().Helper()
	return s.patch(token, "/v1/accounts/"+accountID.String()+"/users/"+userID.String(), body, 200)
}

func (s *AccountActivityTestSuite) patch(token, path, body string, status int) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Patch(path, strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(status)
	return resp
}

func (s *AccountActivityTestSuite) delete(token, path string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Delete(path, nil)
	s.Require().NoError(err)
	return resp
}

func (s *AccountActivityTestSuite) get(token, path string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get(path)
	s.Require().NoError(err)
	return resp
}

func (s *AccountActivityTestSuite) listPlatform(token, query string) activityPage {
	s.T().Helper()
	path := "/v1/platform/activity"
	if query != "" {
		path += "?" + query
	}
	resp := s.get(token, path)
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var page activityPage
	s.Require().NoError(json.Unmarshal([]byte(content), &page))
	return page
}

func (s *AccountActivityTestSuite) list(token string, accountID uuid.UUID, query string) activityPage {
	s.T().Helper()
	path := "/v1/accounts/" + accountID.String() + "/activity"
	if query != "" {
		path += "?" + query
	}
	resp := s.get(token, path)
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var page activityPage
	s.Require().NoError(json.Unmarshal([]byte(content), &page))
	return page
}

func (s *AccountActivityTestSuite) countActivity(action string) int64 {
	s.T().Helper()
	var row struct {
		Count int64 `gorm:"column:count"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT COUNT(*) AS count FROM account_activity WHERE action = ?`,
		action,
	).Scan(&row)
	s.Require().NoError(err)
	return row.Count
}

func (s *AccountActivityTestSuite) pageText(page activityPage) string {
	s.T().Helper()
	encoded, err := json.Marshal(page)
	s.Require().NoError(err)
	return string(encoded)
}

func (s *AccountActivityTestSuite) storedRole(accountID, userID uuid.UUID) (string, string) {
	s.T().Helper()
	var member models.AccountUser
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		First(&member))
	return member.Role, member.Status
}

func (s *AccountActivityTestSuite) storedSecret(accountID uuid.UUID) string {
	s.T().Helper()
	var value string
	err := facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = ? AND "key" = ?`,
		accountID, "account_webhooks", "signing_secret",
	).Scan(&value)
	s.Require().NoError(err)
	return value
}
