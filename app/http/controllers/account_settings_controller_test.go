package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/tests/mocks"
)

const accountSettingsPassword = "correct-horse-battery"

type accountSettingsSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountSettingsSuite(t *testing.T) {
	suite.Run(t, new(accountSettingsSuite))
}

func (s *accountSettingsSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *accountSettingsSuite) TestStoredSweepLimitIsAppliedWhenSweepLoadsLimits() {
	accountID, _ := s.owner()
	sweepService := s.sweepService()

	before, err := sweepService.LoadLimits(context.Background(), accountID)
	s.Require().NoError(err)
	s.Equal(100, before.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(25, before.MaxAddressesPerRequest[models.AdapterTypeSolana])
	s.Equal(50, before.MaxConsolidateReqPerDay)
	s.Nil(before.DailyWithdrawCapUSD)

	_, err = facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'max_consolidate_requests_per_day', '12', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_security', 'require_2fa', 'true', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)

	after, err := sweepService.LoadLimits(context.Background(), accountID)
	s.Require().NoError(err)
	s.Equal(12, after.MaxConsolidateReqPerDay)
	s.Equal(100, after.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(25, after.MaxAddressesPerRequest[models.AdapterTypeSolana])
	s.Equal(100, after.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	s.Nil(after.DailyWithdrawCapUSD)
}

func (s *accountSettingsSuite) sweepService() sweep.Service {
	raw, err := facades.App().Make(container.ContainerKey)
	s.Require().NoError(err)
	vault, ok := raw.(*container.Container)
	s.Require().True(ok)
	s.Require().NotNil(vault.SweepService)
	return vault.SweepService
}

func (s *accountSettingsSuite) TestGetHidesSecretAndShowsIsSet() {
	accountID, token := s.owner()
	s.patch(token, accountID, "account_webhooks", `{"signing_secret":"first-secret"}`, 200)

	body := s.get(token, accountID, 200)
	field := s.field(body, "account_webhooks", "signing_secret")
	s.Equal(true, field["secret"])
	s.Equal(true, field["is_set"])
	_, returned := field["value"]
	s.False(returned)
	s.NotContains(body, "first-secret")
	s.NotContains(body, "enc:v1:")
}

func (s *accountSettingsSuite) TestPatchBlankSecretKeepsTheStoredValue() {
	accountID, token := s.owner()
	s.patch(token, accountID, "account_webhooks", `{"signing_secret":"first-secret"}`, 200)
	before := s.storedSecret(accountID)

	s.patch(token, accountID, "account_webhooks", `{"signing_secret":""}`, 200)
	s.Equal(before, s.storedSecret(accountID))

	s.patch(token, accountID, "account_webhooks", `{"default_events":["deposit.confirmed","withdrawal.confirmed"]}`, 200)
	s.Equal(before, s.storedSecret(accountID))

	plain, err := settings.Open(facades.Crypt(), before)
	s.Require().NoError(err)
	s.Equal("first-secret", plain)
}

func (s *accountSettingsSuite) TestPatchNewSecretIsStoredAndGetHidesIt() {
	accountID, token := s.owner()
	s.patch(token, accountID, "account_webhooks", `{"signing_secret":"second-secret"}`, 200)

	stored := s.storedSecret(accountID)
	s.True(settings.IsSealed(stored))
	s.NotEqual("second-secret", stored)
	plain, err := settings.Open(facades.Crypt(), stored)
	s.Require().NoError(err)
	s.Equal("second-secret", plain)

	body := s.get(token, accountID, 200)
	field := s.field(body, "account_webhooks", "signing_secret")
	s.Equal(true, field["is_set"])
	_, returned := field["value"]
	s.False(returned)
	s.NotContains(body, "second-secret")
}

func (s *accountSettingsSuite) TestPatchAuditorCannotUpdate() {
	accountID, _ := s.owner()
	token := s.member(accountID, "auditor")

	body := s.get(token, accountID, 200)
	field := s.field(body, "account_webhooks", "signing_secret")
	s.Equal(false, field["is_set"])
	s.Equal(false, s.group(body, "account_webhooks")["can_update"])

	response := s.patch(token, accountID, "account_webhooks", `{"signing_secret":"nope"}`, 403)
	s.Equal("forbidden", response["error"].(map[string]any)["code"])
	s.Empty(s.storedSecret(accountID))
}

func (s *accountSettingsSuite) TestPatchUnknownGroupIsNotFound() {
	accountID, token := s.owner()
	response := s.patch(token, accountID, "not-a-group", `{}`, 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])

	auditor := s.member(accountID, "auditor")
	response = s.patch(auditor, accountID, "not-a-group", `{}`, 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])
}

func (s *accountSettingsSuite) TestPatchUserCannotViewOrUpdate() {
	accountID, _ := s.owner()
	token := s.member(accountID, "user")
	s.get(token, accountID, 403)
	s.patch(token, accountID, "account_webhooks", `{"signing_secret":"nope"}`, 403)
}

func (s *accountSettingsSuite) TestGetDecimalTravelsAsString() {
	accountID, token := s.owner()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'daily_withdraw_cap_usd', '12.50', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)

	body := s.get(token, accountID, 200)
	field := s.field(body, "account_sweep_limits", "daily_withdraw_cap_usd")
	s.Equal("12.50", field["value"])
	s.Equal("decimal", field["type"])
	s.Equal(false, s.group(body, "account_sweep_limits")["can_update"])

	response := s.patch(token, accountID, "account_sweep_limits", `{"daily_withdraw_cap_usd":"9.00"}`, 403)
	s.Equal("forbidden", response["error"].(map[string]any)["code"])
}

func (s *accountSettingsSuite) TestPatchUnknownKeyIsValidation() {
	accountID, token := s.owner()
	raw := s.patchRaw(token, accountID, "account_security", `{"not_a_key":"x"}`, 422)
	var body struct {
		Error  map[string]any      `json:"error"`
		Errors map[string][]string `json:"errors"`
	}
	s.Require().NoError(json.Unmarshal([]byte(raw), &body))
	s.Equal("validation_failed", body.Error["code"])
	s.NotEmpty(body.Errors["not_a_key"])
}

func (s *accountSettingsSuite) owner() (uuid.UUID, string) {
	s.T().Helper()
	userID, token := s.user("owner")
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID:          accountID,
		Name:        "Settings " + accountID.String()[:8],
		Status:      "active",
		Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner",
	}))
	return accountID, token
}

func (s *accountSettingsSuite) member(accountID uuid.UUID, role string) string {
	s.T().Helper()
	userID, token := s.user(role)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role,
	}))
	return token
}

func (s *accountSettingsSuite) user(role string) (uuid.UUID, string) {
	s.T().Helper()
	hash, err := authsvc.NewService().HashPassword(accountSettingsPassword)
	s.Require().NoError(err)
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID, s.login(email)
}

func (s *accountSettingsSuite) login(email string) string {
	s.T().Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountSettingsPassword)
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

func (s *accountSettingsSuite) get(token string, accountID uuid.UUID, status int) string {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get("/v1/accounts/" + accountID.String() + "/settings")
	s.Require().NoError(err)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}

func (s *accountSettingsSuite) patch(token string, accountID uuid.UUID, group, body string, status int) map[string]any {
	s.T().Helper()
	raw := s.patchRaw(token, accountID, group, body, status)
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(raw), &parsed))
	return parsed
}

func (s *accountSettingsSuite) patchRaw(token string, accountID uuid.UUID, group, body string, status int) string {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Patch("/v1/accounts/"+accountID.String()+"/settings/"+group, strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}

func (s *accountSettingsSuite) storedSecret(accountID uuid.UUID) string {
	s.T().Helper()
	var value string
	err := facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = ? AND "key" = ?`,
		accountID, "account_webhooks", "signing_secret",
	).Scan(&value)
	s.Require().NoError(err)
	return value
}

func (s *accountSettingsSuite) field(body, groupName, key string) map[string]any {
	s.T().Helper()
	group := s.group(body, groupName)
	fields, _ := group["fields"].([]any)
	for _, item := range fields {
		field, _ := item.(map[string]any)
		if field["key"] == key {
			return field
		}
	}
	s.Failf("missing field", "%s.%s in %s", groupName, key, body)
	return nil
}

func (s *accountSettingsSuite) group(body, name string) map[string]any {
	s.T().Helper()
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(body), &parsed))
	sections, _ := parsed["sections"].([]any)
	for _, sectionItem := range sections {
		section, _ := sectionItem.(map[string]any)
		blocks, _ := section["blocks"].([]any)
		for _, blockItem := range blocks {
			block, _ := blockItem.(map[string]any)
			groups, _ := block["groups"].([]any)
			for _, groupItem := range groups {
				group, _ := groupItem.(map[string]any)
				if group["name"] == name {
					return group
				}
			}
		}
	}
	s.Failf("missing group", "%s in %s", name, body)
	return nil
}
