package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const accountSettingsPassword = "correct-horse-battery"

type accountSettingsSuite struct {
	support.HTTPSuite
}

func TestAccount_Settings_Suite(t *testing.T) {
	support.RunSuite(t, new(accountSettingsSuite))
}

func (s *accountSettingsSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *accountSettingsSuite) TestStored_Sweep_LimitIsAppliedWhenSweepLoadsLimits() {
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

func (s *accountSettingsSuite) TestGet_Hides_SecretAndShowsIsSet() {
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

func (s *accountSettingsSuite) TestGet_Registry_FollowsSettingsRead() {
	accountID, ownerToken := s.owner()
	const sentinel = "view-gate-sentinel"
	s.patch(ownerToken, accountID, "account_webhooks", `{"signing_secret":"`+sentinel+`"}`, 200)

	admin := s.member(accountID, models.AccountRoleAdmin)
	auditor := s.member(accountID, models.AccountRoleAuditor)
	user := s.member(accountID, models.AccountRoleUser)

	for _, token := range []string{ownerToken, admin, auditor} {
		body := s.get(token, accountID, 200)
		s.NotContains(body, sentinel)
		s.NotContains(body, "enc:v1:")
		var parsed struct {
			Permissions struct {
				View   string `json:"view"`
				Update string `json:"update"`
			} `json:"permissions"`
			Sections []any `json:"sections"`
		}
		s.Require().NoError(json.Unmarshal([]byte(body), &parsed))
		s.Equal("settings.read", parsed.Permissions.View)
		s.Equal("settings.write", parsed.Permissions.Update)
		s.Require().NotEmpty(parsed.Sections)
	}

	denied := s.get(user, accountID, 403)
	s.NotContains(denied, sentinel)
	s.NotContains(denied, "enc:v1:")
	s.NotContains(denied, `"sections"`)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Sections []any `json:"sections"`
	}
	s.Require().NoError(json.Unmarshal([]byte(denied), &parsed))
	s.AssertError(support.BodyRecorder(403, denied), 403, "forbidden", settings.ErrViewForbidden.Error())
	s.Empty(parsed.Sections)
}

func (s *accountSettingsSuite) TestPatch_Blank_SecretKeepsTheStoredValue() {
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

func (s *accountSettingsSuite) TestPatch_New_SecretIsStoredAndGetHidesIt() {
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

func (s *accountSettingsSuite) TestPatch_Auditor_CannotUpdate() {
	accountID, _ := s.owner()
	token := s.member(accountID, "auditor")

	body := s.get(token, accountID, 200)
	field := s.field(body, "account_webhooks", "signing_secret")
	s.Equal(false, field["is_set"])
	s.Equal(false, s.group(body, "account_webhooks")["can_update"])

	response := s.patch(token, accountID, "account_webhooks", `{"signing_secret":"nope"}`, 403)
	encoded, err := json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(403, string(encoded)), 403, "forbidden", settings.ErrUpdateForbidden.Error())
	s.Empty(s.storedSecret(accountID))

	response = s.put(token, accountID, "account_webhooks", `{"signing_secret":"nope"}`, 403)
	encoded, err = json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(403, string(encoded)), 403, "forbidden", settings.ErrUpdateForbidden.Error())
	s.Empty(s.storedSecret(accountID))
}

func (s *accountSettingsSuite) TestAdmin_Can_UpdateAnAccountGroup() {
	accountID, _ := s.owner()
	admin := s.member(accountID, models.AccountRoleAdmin)

	saved := s.patchRaw(admin, accountID, "account_security", `{"session_idle_minutes":45}`, 200)
	s.NotContains(saved, "enc:v1:")
	s.Equal(float64(45), s.groupField(saved, "session_idle_minutes")["value"])

	saved = s.putRaw(admin, accountID, "account_security", `{"session_idle_minutes":50}`, 200)
	s.NotContains(saved, "enc:v1:")
	s.Equal(float64(50), s.groupField(saved, "session_idle_minutes")["value"])
}

func (s *accountSettingsSuite) TestPatch_Unknown_GroupIsNotFound() {
	accountID, token := s.owner()
	response := s.patch(token, accountID, "not-a-group", `{}`, 404)
	encoded, err := json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(404, string(encoded)), 404, "not_found", "settings group not found")

	auditor := s.member(accountID, "auditor")
	denied := s.patchRaw(auditor, accountID, "not-a-group", `{}`, 404)
	s.NotContains(denied, `"fields"`)
	s.NotContains(denied, "enc:v1:")
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Fields []any `json:"fields"`
	}
	s.Require().NoError(json.Unmarshal([]byte(denied), &parsed))
	s.AssertError(support.BodyRecorder(404, denied), 404, "not_found", "settings group not found")
	s.Empty(parsed.Fields)
	s.Empty(s.storedSecret(accountID))
}

func (s *accountSettingsSuite) TestPatch_User_CannotViewOrUpdate() {
	accountID, _ := s.owner()
	token := s.member(accountID, "user")
	s.get(token, accountID, 403)
	denied := s.patchRaw(token, accountID, "account_webhooks", `{"signing_secret":"nope"}`, 403)
	s.NotContains(denied, "nope")
	s.NotContains(denied, `"fields"`)
	s.NotContains(denied, "enc:v1:")
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Fields []any `json:"fields"`
	}
	s.Require().NoError(json.Unmarshal([]byte(denied), &parsed))
	s.AssertError(support.BodyRecorder(403, denied), 403, "forbidden", settings.ErrUpdateForbidden.Error())
	s.Empty(parsed.Fields)
	s.Empty(s.storedSecret(accountID))

	denied = s.putRaw(token, accountID, "account_webhooks", `{"signing_secret":"nope"}`, 403)
	s.NotContains(denied, "nope")
	s.NotContains(denied, `"fields"`)
	s.NotContains(denied, "enc:v1:")
	s.Require().NoError(json.Unmarshal([]byte(denied), &parsed))
	s.AssertError(support.BodyRecorder(403, denied), 403, "forbidden", settings.ErrUpdateForbidden.Error())
	s.Empty(parsed.Fields)
	s.Empty(s.storedSecret(accountID))
}

func (s *accountSettingsSuite) TestGet_Decimal_TravelsAsString() {
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
	encoded, err := json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(403, string(encoded)), 403, "forbidden", settings.ErrManagedByPlatform.Error())
}

func (s *accountSettingsSuite) TestReset_Section_ClearsThePageAndRecordsFieldNames() {
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
	s.Require().NotEmpty(keptSecret)
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

	var removed []struct {
		Scope      string `gorm:"column:scope"`
		Properties string `gorm:"column:properties"`
	}
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT scope, properties::text AS properties
		 FROM activity_log
		 WHERE subject_type = 'setting' AND event = 'deleted'
		   AND properties->'old'->>'group' = 'account_webhooks'
		   AND properties->'old'->>'key' = 'signing_secret'`,
	).Scan(&removed))
	s.Require().Len(removed, 1)
	s.Equal("account:"+accountID.String(), removed[0].Scope)
	if strings.Contains(removed[0].Properties, "reset-me-secret") || strings.Contains(removed[0].Properties, "enc:v1:") {
		s.Fail("activity log stored a settings secret")
	}
	var removedProps struct {
		Old map[string]any `json:"old"`
	}
	s.Require().NoError(json.Unmarshal([]byte(removed[0].Properties), &removedProps))
	s.Equal(true, removedProps.Old["valueSet"])
	_, hasValue := removedProps.Old["value"]
	s.False(hasValue)
}

func (s *accountSettingsSuite) TestFlush_Section_LeavesStoredRowsAndDropsOnlyThatPageCache() {
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

func (s *accountSettingsSuite) TestFlush_Unknown_SectionIsNotFoundBeforeForbidden() {
	accountID, token := s.owner()
	securityKey := accountSettingsCacheKey(accountID, "account_security")
	s.T().Cleanup(func() { facades.Cache().Forget(securityKey) })
	s.Require().NoError(facades.Cache().Put(securityKey, "stale-security", 10*time.Minute))

	response := s.flushParsed(token, accountID, "not-a-section", 404)
	encoded, err := json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(404, string(encoded)), 404, "not_found", "settings section not found")
	s.assertCacheKeySurvived(securityKey, "stale-security")

	admin := s.member(accountID, models.AccountRoleAdmin)
	s.Require().NoError(facades.Cache().Put(securityKey, "stale-security", 10*time.Minute))
	body := s.flush(admin, accountID, "security", 204)
	s.Empty(strings.TrimSpace(body))
	s.False(facades.Cache().Has(securityKey))

	s.Require().NoError(facades.Cache().Put(securityKey, "stale-security", 10*time.Minute))
	auditor := s.member(accountID, "auditor")
	s.assertFlushMissing(auditor, accountID, "scanning", securityKey)
	s.assertFlushWriteDenied(auditor, accountID, "security", securityKey)

	user := s.member(accountID, "user")
	s.assertFlushMissing(user, accountID, "not-a-section", securityKey)
	s.assertFlushWriteDenied(user, accountID, "security", securityKey)
}

func (s *accountSettingsSuite) assertFlushMissing(token string, accountID uuid.UUID, section, securityKey string) {
	s.T().Helper()
	parsed := s.flushParsed(token, accountID, section, 404)
	encoded, err := json.Marshal(parsed)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(404, string(encoded)), 404, "not_found", "settings section not found")
	s.assertCacheKeySurvived(securityKey, "stale-security")
}

func (s *accountSettingsSuite) assertFlushWriteDenied(token string, accountID uuid.UUID, section, securityKey string) {
	s.T().Helper()
	raw := s.flush(token, accountID, section, 403)
	s.NotContains(raw, "enc:v1:")
	s.NotContains(raw, "stale-security")
	s.AssertError(support.BodyRecorder(403, raw), 403, "forbidden", settings.ErrUpdateForbidden.Error())
	s.assertCacheKeySurvived(securityKey, "stale-security")
}

// assertCacheKeySurvived checks a refused flush left the key. A settings read
// on the request replaces an unsealed sentinel with the stored JSON document
// and does not delete the key. The document is not written into the failure.
func (s *accountSettingsSuite) assertCacheKeySurvived(key, sentinel string) {
	s.T().Helper()
	if !facades.Cache().Has(key) {
		s.Fail("refused flush removed the cache key")
		return
	}
	value := facades.Cache().GetString(key)
	if value == sentinel || settings.IsSealed(value) || json.Valid([]byte(value)) {
		return
	}
	s.Fail("refused flush left a cache value that is neither the sentinel nor a settings document")
}

func (s *accountSettingsSuite) TestFlush_Platform_ManagedSectionIsForbidden() {
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
	encoded, err := json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(403, string(encoded)), 403, "forbidden", settings.ErrManagedByPlatform.Error())
	s.Equal("stale-limits", facades.Cache().GetString(limitsKey))

	var cap string
	err = facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND "key" = 'daily_withdraw_cap_usd'`,
		accountID,
	).Scan(&cap)
	s.Require().NoError(err)
	s.Equal("12.50", cap)
}

func (s *accountSettingsSuite) TestReset_Unknown_SectionIsNotFoundBeforeForbidden() {
	accountID, token := s.owner()
	s.insertSecurityIdle(accountID, "45")

	response := s.resetParsed(token, accountID, "not-a-section", 404)
	encoded, err := json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(404, string(encoded)), 404, "not_found", "settings section not found")
	s.assertSecurityIdle(accountID, "45")

	admin := s.member(accountID, models.AccountRoleAdmin)
	raw := s.reset(admin, accountID, "security", 200)
	s.NotContains(raw, "enc:v1:")
	s.assertSecurityIdleAbsent(accountID)

	s.insertSecurityIdle(accountID, "45")
	var resets int64
	err = facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity WHERE account_id = ? AND action = 'settings.section_reset'`,
		accountID,
	).Scan(&resets)
	s.Require().NoError(err)

	auditor := s.member(accountID, "auditor")
	s.assertResetMissing(auditor, accountID, "scanning")
	s.assertResetWriteDenied(auditor, accountID, "security")

	user := s.member(accountID, "user")
	s.assertResetMissing(user, accountID, "not-a-section")
	s.assertResetWriteDenied(user, accountID, "security")
	s.assertSecurityIdle(accountID, "45")

	var after int64
	err = facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity WHERE account_id = ? AND action = 'settings.section_reset'`,
		accountID,
	).Scan(&after)
	s.Require().NoError(err)
	s.Equal(resets, after)
}

func (s *accountSettingsSuite) assertResetMissing(token string, accountID uuid.UUID, section string) {
	s.T().Helper()
	parsed := s.resetParsed(token, accountID, section, 404)
	encoded, err := json.Marshal(parsed)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(404, string(encoded)), 404, "not_found", "settings section not found")
	s.assertSecurityIdle(accountID, "45")
}

func (s *accountSettingsSuite) assertResetWriteDenied(token string, accountID uuid.UUID, section string) {
	s.T().Helper()
	raw := s.reset(token, accountID, section, 403)
	s.NotContains(raw, "enc:v1:")
	s.AssertError(support.BodyRecorder(403, raw), 403, "forbidden", settings.ErrUpdateForbidden.Error())
	s.assertSecurityIdle(accountID, "45")
}

func (s *accountSettingsSuite) insertSecurityIdle(accountID uuid.UUID, value string) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_security', 'session_idle_minutes', ?, NOW(), NOW())`,
		accountID, value,
	)
	s.Require().NoError(err)
}

func (s *accountSettingsSuite) assertSecurityIdle(accountID uuid.UUID, value string) {
	s.T().Helper()
	var stored string
	err := facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_security' AND "key" = 'session_idle_minutes'`,
		accountID,
	).Scan(&stored)
	s.Require().NoError(err)
	s.Equal(value, stored)
}

func (s *accountSettingsSuite) assertSecurityIdleAbsent(accountID uuid.UUID) {
	s.T().Helper()
	var rows int64
	err := facades.Orm().Query().Raw(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_security'`,
		accountID,
	).Scan(&rows)
	s.Require().NoError(err)
	s.Equal(int64(0), rows)
}

func (s *accountSettingsSuite) TestReset_Platform_ManagedSectionIsForbidden() {
	accountID, token := s.owner()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'daily_withdraw_cap_usd', '12.50', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)

	response := s.resetParsed(token, accountID, "limits", 403)
	encoded, err := json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(403, string(encoded)), 403, "forbidden", settings.ErrManagedByPlatform.Error())

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

func (s *accountSettingsSuite) TestPatch_Unknown_KeyIsValidation() {
	accountID, token := s.owner()
	raw := s.patchRaw(token, accountID, "account_security", `{"not_a_key":"x"}`, 422)
	s.AssertError(support.BodyRecorder(422, raw), 422, "validation_failed", "validation failed")
	var body struct {
		Errors map[string][]string `json:"errors"`
	}
	s.Require().NoError(json.Unmarshal([]byte(raw), &body))
	s.NotEmpty(body.Errors["not_a_key"])
}

func (s *accountSettingsSuite) TestGet_Group_ReadsOneAccountAndHidesTheSecret() {
	accountID, token := s.owner()
	otherID, _ := s.owner()
	secret := "group-read-secret"
	s.patch(token, accountID, "account_webhooks", fmt.Sprintf(`{"signing_secret":%q}`, secret), 200)
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'max_addresses_evm', '17', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'max_addresses_evm', '19', NOW(), NOW())`,
		otherID,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'daily_withdraw_cap_usd', '8.75', NOW(), NOW())`,
		otherID,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_webhooks', 'signing_secret', 'enc:v1:other-account', NOW(), NOW())`,
		otherID,
	)
	s.Require().NoError(err)

	var before int64
	err = facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity WHERE account_id = ?`,
		accountID,
	).Scan(&before)
	s.Require().NoError(err)

	raw := s.getGroup(token, accountID, "account_sweep_limits", 200)
	s.NotContains(raw, secret)
	s.NotContains(raw, "enc:v1:")
	s.NotContains(raw, "8.75")
	s.NotContains(raw, "other-account")
	s.Equal(float64(17), s.groupField(raw, "max_addresses_evm")["value"])
	s.Equal(false, s.groupDocument(raw)["can_update"])
	s.Equal("platform", s.groupDocument(raw)["managed_by"])

	webhooks := s.getGroup(token, accountID, "account_webhooks", 200)
	s.NotContains(webhooks, secret)
	s.NotContains(webhooks, "enc:v1:")
	field := s.groupField(webhooks, "signing_secret")
	s.Equal(true, field["secret"])
	s.Equal(true, field["is_set"])
	_, returned := field["value"]
	s.False(returned)
	s.Equal(true, s.groupDocument(webhooks)["can_update"])

	auditor := s.member(accountID, "auditor")
	audited := s.getGroup(auditor, accountID, "account_webhooks", 200)
	s.NotContains(audited, secret)
	s.NotContains(audited, "enc:v1:")
	s.Equal(false, s.groupDocument(audited)["can_update"])

	admin := s.member(accountID, models.AccountRoleAdmin)
	adminView := s.getGroup(admin, accountID, "account_webhooks", 200)
	s.NotContains(adminView, secret)
	s.NotContains(adminView, "enc:v1:")
	s.Equal(true, s.groupDocument(adminView)["can_update"])

	user := s.member(accountID, models.AccountRoleUser)
	denied := s.getGroup(user, accountID, "account_webhooks", 403)
	s.NotContains(denied, secret)
	s.NotContains(denied, "enc:v1:")
	s.NotContains(denied, `"fields"`)
	var deniedBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Fields []any `json:"fields"`
	}
	s.Require().NoError(json.Unmarshal([]byte(denied), &deniedBody))
	s.AssertError(support.BodyRecorder(403, denied), 403, "forbidden", settings.ErrViewForbidden.Error())
	s.Empty(deniedBody.Fields)

	var after int64
	err = facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity WHERE account_id = ?`,
		accountID,
	).Scan(&after)
	s.Require().NoError(err)
	s.Equal(before, after)
}

func (s *accountSettingsSuite) TestGet_Group_UnknownIsNotFoundBeforeForbidden() {
	accountID, token := s.owner()
	response := s.getGroupParsed(token, accountID, "not-a-group", 404)
	s.assertMapError(response, 404, "not_found", "settings group not found")
	response = s.getGroupParsed(token, accountID, "mail_smtp", 404)
	s.assertMapError(response, 404, "not_found", "settings group not found")

	auditor := s.member(accountID, "auditor")
	response = s.getGroupParsed(auditor, accountID, "sweep_limits", 404)
	s.assertMapError(response, 404, "not_found", "settings group not found")
	limits := s.getGroup(auditor, accountID, "account_sweep_limits", 200)
	s.Equal(false, s.groupDocument(limits)["can_update"])

	user := s.member(accountID, "user")
	for _, group := range []string{"not-a-group", "deposit_scan"} {
		missing := s.getGroupParsed(user, accountID, group, 404)
		encoded, err := json.Marshal(missing)
		s.Require().NoError(err)
		s.AssertError(support.BodyRecorder(404, string(encoded)), 404, "not_found", "settings group not found")
	}
	for _, group := range []string{"account_security"} {
		denied := s.getGroup(user, accountID, group, 403)
		s.NotContains(denied, `"fields"`)
		s.NotContains(denied, "enc:v1:")
		var parsed struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Fields []any `json:"fields"`
		}
		s.Require().NoError(json.Unmarshal([]byte(denied), &parsed))
		s.AssertError(support.BodyRecorder(403, denied), 403, "forbidden", settings.ErrViewForbidden.Error())
		s.Empty(parsed.Fields)
	}
}

func (s *accountSettingsSuite) TestPut_Shares_ThePatchBodyRules() {
	accountID, token := s.owner()
	secret := "put-keeps-secret"
	saved := s.putRaw(token, accountID, "account_webhooks", fmt.Sprintf(`{"signing_secret":%q}`, secret), 200)
	s.NotContains(saved, secret)
	before := s.storedSecret(accountID)
	s.Require().NotEmpty(before)
	s.NotEqual(secret, before)

	kept := s.putRaw(token, accountID, "account_webhooks", `{"signing_secret":""}`, 200)
	s.NotContains(kept, secret)
	s.Equal(before, s.storedSecret(accountID))

	s.put(token, accountID, "account_security", `{"session_idle_minutes":45}`, 200)
	idle := s.getGroup(token, accountID, "account_security", 200)
	s.Equal(float64(45), s.groupField(idle, "session_idle_minutes")["value"])

	rejected := s.putRaw(token, accountID, "account_security", `{"session_idle_minutes":-1}`, 422)
	s.NotContains(rejected, secret)
	s.AssertError(support.BodyRecorder(422, rejected), 422, "validation_failed", "validation failed")
	idle = s.getGroup(token, accountID, "account_security", 200)
	s.Equal(float64(45), s.groupField(idle, "session_idle_minutes")["value"])

	response := s.put(token, accountID, "account_sweep_limits", `{"daily_withdraw_cap_usd":"-1"}`, 403)
	s.assertMapError(response, 403, "forbidden", settings.ErrManagedByPlatform.Error())
	var caps int64
	err := facades.Orm().Query().Raw(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND "key" = 'daily_withdraw_cap_usd'`,
		accountID,
	).Scan(&caps)
	s.Require().NoError(err)
	s.Equal(int64(0), caps)

	user := s.member(accountID, "user")
	response = s.put(user, accountID, "not-a-group", `{}`, 404)
	encoded, err := json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(404, string(encoded)), 404, "not_found", "settings group not found")
	response = s.put(user, accountID, "account_security", `{"session_idle_minutes":12}`, 403)
	encoded, err = json.Marshal(response)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(403, string(encoded)), 403, "forbidden", settings.ErrUpdateForbidden.Error())
	idle = s.getGroup(token, accountID, "account_security", 200)
	s.Equal(float64(45), s.groupField(idle, "session_idle_minutes")["value"])
}

func (s *accountSettingsSuite) TestAn_Account_SaveRecordsAccountScopeAndValueSet() {
	accountID, token := s.owner()
	otherID, otherToken := s.owner()
	const secret = "account-scope-audit-marker"
	saved := s.putRaw(token, accountID, "account_webhooks", fmt.Sprintf(`{"signing_secret":%q}`, secret), 200)
	s.NotContains(saved, secret)
	s.NotContains(saved, "enc:v1:")

	var trails []struct {
		Scope      string `gorm:"column:scope"`
		Event      string `gorm:"column:event"`
		Properties string `gorm:"column:properties"`
	}
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT scope, event, properties::text AS properties
		 FROM activity_log
		 WHERE subject_type = 'setting'
		   AND properties->'new'->>'group' = 'account_webhooks'
		   AND properties->'new'->>'key' = 'signing_secret'`,
	).Scan(&trails))
	s.Require().Len(trails, 1)
	s.Equal("account:"+accountID.String(), trails[0].Scope)
	s.Equal("created", trails[0].Event)
	if strings.Contains(trails[0].Properties, secret) || strings.Contains(trails[0].Properties, "enc:v1:") {
		s.Fail("activity log stored a settings secret")
	}
	var props struct {
		New map[string]any `json:"new"`
	}
	s.Require().NoError(json.Unmarshal([]byte(trails[0].Properties), &props))
	s.Equal(true, props.New["valueSet"])
	s.Equal("signing_secret", props.New["key"])
	_, hasValue := props.New["value"]
	s.False(hasValue)

	var onAccount int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity
		 WHERE account_id = ? AND action = 'settings.updated' AND target_id = 'account_webhooks'`,
		accountID,
	).Scan(&onAccount))
	s.Equal(int64(1), onAccount)
	var onPlatform int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity
		 WHERE account_id IS NULL AND action = 'settings.updated' AND target_id = 'account_webhooks'`,
	).Scan(&onPlatform))
	s.Equal(int64(0), onPlatform)

	ownList := s.activityPage(token, accountID)
	s.True(activityHasTarget(ownList, "settings.updated", "account_webhooks"))
	otherList := s.activityPage(otherToken, otherID)
	s.False(activityHasTarget(otherList, "settings.updated", "account_webhooks"))
}

func (s *accountSettingsSuite) activityPage(token string, accountID uuid.UUID) map[string]any {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String()+"/activity", support.Session{AccessToken: token})
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var page map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &page))
	if strings.Contains(content, "account-scope-audit-marker") || strings.Contains(content, "enc:v1:") {
		s.Fail("account activity list stored a settings secret")
	}
	return page
}

func activityHasTarget(page map[string]any, action, target string) bool {
	rows, _ := page["data"].([]any)
	for _, item := range rows {
		row, _ := item.(map[string]any)
		if row["action"] == action && row["target_id"] == target {
			return true
		}
	}
	return false
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
	hash, err := authsvc.NewService(appfacades.Hash()).HashPassword(accountSettingsPassword)
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

func (s *accountSettingsSuite) getGroup(token string, accountID uuid.UUID, group string, status int) string {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String()+"/settings/"+group, support.Session{AccessToken: token})
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}

func (s *accountSettingsSuite) getGroupParsed(token string, accountID uuid.UUID, group string, status int) map[string]any {
	s.T().Helper()
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(s.getGroup(token, accountID, group, status)), &parsed))
	return parsed
}

func (s *accountSettingsSuite) put(token string, accountID uuid.UUID, group, body string, status int) map[string]any {
	s.T().Helper()
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(s.putRaw(token, accountID, group, body, status)), &parsed))
	return parsed
}

func (s *accountSettingsSuite) putRaw(token string, accountID uuid.UUID, group, body string, status int) string {
	s.T().Helper()
	resp := s.Put("/v1/accounts/"+accountID.String()+"/settings/"+group, support.Session{AccessToken: token}, body)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}

func (s *accountSettingsSuite) groupDocument(body string) map[string]any {
	s.T().Helper()
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(body), &parsed))
	return parsed
}

func (s *accountSettingsSuite) groupField(body, key string) map[string]any {
	s.T().Helper()
	fields, _ := s.groupDocument(body)["fields"].([]any)
	for _, item := range fields {
		field, _ := item.(map[string]any)
		if field["key"] == key {
			return field
		}
	}
	s.Failf("missing field", "%s in %s", key, body)
	return nil
}

func (s *accountSettingsSuite) get(token string, accountID uuid.UUID, status int) string {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String()+"/settings", support.Session{AccessToken: token})
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	if status != 200 {
		s.AssertError(resp, status, "forbidden", settings.ErrViewForbidden.Error())
	}
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
	resp := s.Patch("/v1/accounts/"+accountID.String()+"/settings/"+group, support.Session{AccessToken: token}, body)
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
	resp := s.Post("/v1/accounts/"+accountID.String()+"/settings/sections/"+section+"/cache", support.Session{AccessToken: token}, "{}")
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
	resp := s.Post("/v1/accounts/"+accountID.String()+"/settings/sections/"+section+"/reset", support.Session{AccessToken: token}, "{}")
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

func (s *accountSettingsSuite) assertMapError(body map[string]any, status int, code, message string) {
	s.T().Helper()
	encoded, err := json.Marshal(body)
	s.Require().NoError(err)
	s.AssertError(support.BodyRecorder(status, string(encoded)), status, code, message)
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
