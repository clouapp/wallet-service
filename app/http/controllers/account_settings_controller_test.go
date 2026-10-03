package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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
	// The first load cached the empty group. An insert outside the service
	// stays invisible until that key is forgotten, which is what Flush does.
	s.True(facades.Cache().Forget(accountSettingsCacheKey(accountID, "account_sweep_limits")))

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

func (s *accountSettingsSuite) TestResetSectionClearsThePageAndRecordsFieldNames() {
	accountID, token := s.owner()
	otherID, _ := s.owner()
	s.patch(token, accountID, "account_webhooks", `{"signing_secret":"reset-me-secret"}`, 200)
	s.patch(token, accountID, "account_security", `{"session_idle_minutes":45}`, 200)
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'max_addresses_evm', '7', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_security', 'require_2fa', 'true', NOW(), NOW())`,
		otherID,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, 'deposit_scan', 'batch_blocks', '80', NOW(), NOW())`,
	)
	s.Require().NoError(err)

	raw := s.reset(token, accountID, "security", 200)
	s.NotContains(raw, "reset-me-secret")
	s.NotContains(raw, "enc:v1:")
	var view struct {
		Name   string `json:"name"`
		Blocks []struct {
			Groups []struct {
				Name   string `json:"name"`
				Fields []struct {
					Key   string `json:"key"`
					Value any    `json:"value"`
					IsSet bool   `json:"is_set"`
				} `json:"fields"`
			} `json:"groups"`
		} `json:"blocks"`
	}
	s.Require().NoError(json.Unmarshal([]byte(raw), &view))
	s.Equal("security", view.Name)
	s.Require().NotEmpty(view.Blocks)
	s.Equal("account_security", view.Blocks[0].Groups[0].Name)
	for _, field := range view.Blocks[0].Groups[0].Fields {
		if field.Key == "session_idle_minutes" {
			s.Equal(float64(30), field.Value)
			s.False(field.IsSet)
		}
		if field.Key == "require_2fa" {
			s.Equal(false, field.Value)
			s.False(field.IsSet)
		}
	}

	var securityRows int64
	err = facades.Orm().Query().Raw(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_security'`,
		accountID,
	).Scan(&securityRows)
	s.Require().NoError(err)
	s.Equal(int64(0), securityRows)

	var other string
	err = facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_security' AND "key" = 'require_2fa'`,
		otherID,
	).Scan(&other)
	s.Require().NoError(err)
	s.Equal("true", other)

	var sweep string
	err = facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND "key" = 'max_addresses_evm'`,
		accountID,
	).Scan(&sweep)
	s.Require().NoError(err)
	s.Equal("7", sweep)

	var scan string
	err = facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'deposit_scan' AND "key" = 'batch_blocks'`,
	).Scan(&scan)
	s.Require().NoError(err)
	s.Equal("80", scan)

	var meta string
	err = facades.Orm().Query().Raw(
		`SELECT metadata::text FROM account_activity WHERE account_id = ? AND action = 'settings.section_reset'`,
		accountID,
	).Scan(&meta)
	s.Require().NoError(err)
	s.Contains(meta, `"account_security"`)
	s.Contains(meta, `"require_2fa"`)
	s.Contains(meta, `"session_idle_minutes"`)
	s.NotContains(meta, "reset-me-secret")
	s.NotContains(meta, "enc:v1:")
	s.NotContains(meta, "45")

	keptSecret := s.storedSecret(accountID)
	s.NotEmpty(keptSecret)
	s.NotEqual("reset-me-secret", keptSecret)

	webhooks := s.reset(token, accountID, "webhooks", 200)
	s.NotContains(webhooks, "reset-me-secret")
	s.NotContains(webhooks, keptSecret)
	var secretRows int64
	err = facades.Orm().Query().Raw(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_webhooks'`,
		accountID,
	).Scan(&secretRows)
	s.Require().NoError(err)
	s.Equal(int64(0), secretRows)

	var webhookMeta string
	err = facades.Orm().Query().Raw(
		`SELECT metadata::text FROM account_activity WHERE account_id = ? AND action = 'settings.section_reset' AND target_id = 'webhooks'`,
		accountID,
	).Scan(&webhookMeta)
	s.Require().NoError(err)
	s.Contains(webhookMeta, `"account_webhooks"`)
	s.Contains(webhookMeta, `"signing_secret"`)
	s.NotContains(webhookMeta, "reset-me-secret")
	s.NotContains(webhookMeta, keptSecret)
	s.NotContains(webhookMeta, "enc:v1:")
}

func (s *accountSettingsSuite) TestFlushSectionLeavesStoredRowsAndDropsOnlyThatPageCache() {
	accountID, token := s.owner()
	s.patch(token, accountID, "account_security", `{"session_idle_minutes":45}`, 200)
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'max_addresses_evm', '7', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)

	var before int64
	err = facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity WHERE account_id = ?`,
		accountID,
	).Scan(&before)
	s.Require().NoError(err)

	securityKey := accountSettingsCacheKey(accountID, "account_security")
	webhooksKey := accountSettingsCacheKey(accountID, "account_webhooks")
	limitsKey := accountSettingsCacheKey(accountID, "account_sweep_limits")
	s.T().Cleanup(func() {
		facades.Cache().Forget(securityKey)
		facades.Cache().Forget(webhooksKey)
		facades.Cache().Forget(limitsKey)
	})
	s.Require().NoError(facades.Cache().Put(securityKey, "stale-security", 10*time.Minute))
	s.Require().NoError(facades.Cache().Put(webhooksKey, "stale-webhooks", 10*time.Minute))
	s.Require().NoError(facades.Cache().Put(limitsKey, "stale-limits", 10*time.Minute))
	s.True(facades.Cache().Has(securityKey))

	body := s.flush(token, accountID, "security", 204)
	s.Empty(strings.TrimSpace(body))
	s.False(facades.Cache().Has(securityKey))
	s.Equal("stale-webhooks", facades.Cache().GetString(webhooksKey))
	s.Equal("stale-limits", facades.Cache().GetString(limitsKey))

	var idle string
	err = facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_security' AND "key" = 'session_idle_minutes'`,
		accountID,
	).Scan(&idle)
	s.Require().NoError(err)
	s.Equal("45", idle)

	var sweep string
	err = facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND "key" = 'max_addresses_evm'`,
		accountID,
	).Scan(&sweep)
	s.Require().NoError(err)
	s.Equal("7", sweep)

	var after int64
	err = facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity WHERE account_id = ?`,
		accountID,
	).Scan(&after)
	s.Require().NoError(err)
	s.Equal(before, after)
}

func (s *accountSettingsSuite) TestFlushUnknownSectionIsNotFoundBeforeForbidden() {
	accountID, token := s.owner()
	securityKey := accountSettingsCacheKey(accountID, "account_security")
	s.T().Cleanup(func() { facades.Cache().Forget(securityKey) })
	s.Require().NoError(facades.Cache().Put(securityKey, "stale-security", 10*time.Minute))

	response := s.flushParsed(token, accountID, "not-a-section", 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])
	s.assertCacheKeySurvived(securityKey, "stale-security")

	auditor := s.member(accountID, "auditor")
	response = s.flushParsed(auditor, accountID, "scanning", 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])
	s.assertCacheKeySurvived(securityKey, "stale-security")

	user := s.member(accountID, "user")
	response = s.flushParsed(user, accountID, "not-a-section", 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])
	response = s.flushParsed(user, accountID, "security", 403)
	s.Equal("forbidden", response["error"].(map[string]any)["code"])
	s.assertCacheKeySurvived(securityKey, "stale-security")
}

// assertCacheKeySurvived checks a refused flush left the key. A settings read
// on the request replaces an unsealed sentinel with a sealed document and
// does not delete the key. The sealed blob is not written into the failure.
func (s *accountSettingsSuite) assertCacheKeySurvived(key, sentinel string) {
	s.T().Helper()
	if !facades.Cache().Has(key) {
		s.Fail("refused flush removed the cache key")
		return
	}
	value := facades.Cache().GetString(key)
	if value == sentinel || settings.IsSealed(value) {
		return
	}
	s.Fail("refused flush left a cache value that is neither the sentinel nor sealed")
}

func (s *accountSettingsSuite) TestFlushPlatformManagedSectionIsForbidden() {
	accountID, token := s.owner()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'daily_withdraw_cap_usd', '12.50', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)
	limitsKey := accountSettingsCacheKey(accountID, "account_sweep_limits")
	s.T().Cleanup(func() { facades.Cache().Forget(limitsKey) })
	s.Require().NoError(facades.Cache().Put(limitsKey, "stale-limits", 10*time.Minute))

	response := s.flushParsed(token, accountID, "limits", 403)
	s.Equal("forbidden", response["error"].(map[string]any)["code"])
	s.Equal("stale-limits", facades.Cache().GetString(limitsKey))

	var cap string
	err = facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND "key" = 'daily_withdraw_cap_usd'`,
		accountID,
	).Scan(&cap)
	s.Require().NoError(err)
	s.Equal("12.50", cap)
}

func (s *accountSettingsSuite) TestResetUnknownSectionIsNotFoundBeforeForbidden() {
	accountID, token := s.owner()
	response := s.resetParsed(token, accountID, "not-a-section", 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])

	auditor := s.member(accountID, "auditor")
	response = s.resetParsed(auditor, accountID, "scanning", 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])

	user := s.member(accountID, "user")
	response = s.resetParsed(user, accountID, "not-a-section", 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])
	response = s.resetParsed(user, accountID, "security", 403)
	s.Equal("forbidden", response["error"].(map[string]any)["code"])
}

func (s *accountSettingsSuite) TestResetPlatformManagedSectionIsForbidden() {
	accountID, token := s.owner()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'daily_withdraw_cap_usd', '12.50', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)

	response := s.resetParsed(token, accountID, "limits", 403)
	s.Equal("forbidden", response["error"].(map[string]any)["code"])

	var cap string
	err = facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND "key" = 'daily_withdraw_cap_usd'`,
		accountID,
	).Scan(&cap)
	s.Require().NoError(err)
	s.Equal("12.50", cap)

	var resets int64
	err = facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity WHERE account_id = ? AND action = 'settings.section_reset'`,
		accountID,
	).Scan(&resets)
	s.Require().NoError(err)
	s.Equal(int64(0), resets)
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

func accountSettingsCacheKey(accountID uuid.UUID, group string) string {
	return "settings:account:" + accountID.String() + ":" + group
}

func (s *accountSettingsSuite) flush(token string, accountID uuid.UUID, section string, status int) string {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Post("/v1/accounts/"+accountID.String()+"/settings/sections/"+section+"/cache", strings.NewReader("{}"))
	s.Require().NoError(err)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}

func (s *accountSettingsSuite) flushParsed(token string, accountID uuid.UUID, section string, status int) map[string]any {
	s.T().Helper()
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(s.flush(token, accountID, section, status)), &parsed))
	return parsed
}

func (s *accountSettingsSuite) reset(token string, accountID uuid.UUID, section string, status int) string {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Post("/v1/accounts/"+accountID.String()+"/settings/sections/"+section+"/reset", strings.NewReader("{}"))
	s.Require().NoError(err)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}

func (s *accountSettingsSuite) resetParsed(token string, accountID uuid.UUID, section string, status int) map[string]any {
	s.T().Helper()
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(s.reset(token, accountID, section, status)), &parsed))
	return parsed
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
