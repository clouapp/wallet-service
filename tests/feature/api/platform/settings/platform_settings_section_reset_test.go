package settings

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const (
	platformResetStoredHost = "smtp.example.test"
	platformResetSecret     = "platform-section-reset-secret"
)

// PlatformSettingsSectionResetTestSuite is POST /v1/platform/settings/sections/{section}/reset
// from S1.4.6. settings.update is not in a platform catalog, so a platform_admins
// row is the gate. An unknown section is 404 before that 403.
type PlatformSettingsSectionResetTestSuite struct {
	authSuite
}

func TestPlatform_Settings_SectionResetSuite(t *testing.T) {
	suite.Run(t, new(PlatformSettingsSectionResetTestSuite))
}

func (s *PlatformSettingsSectionResetTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformSettingsSectionResetTestSuite) TestAdmin_Reset_DeletesPlatformRowsAndTheNextReadIsTheDefault() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountID := s.ownAccount(admin.ID)

	s.exec(
		`DELETE FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp'`,
	)
	s.exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, 'mail_smtp', 'host', ?, NOW(), NOW()),
		        (NULL, 'mail_smtp', 'password', ?, NOW(), NOW())`,
		platformResetStoredHost, "enc:v1:"+platformResetSecret,
	)
	s.exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, 'deposit_scan', 'batch_blocks', '80', NOW(), NOW())
		 ON CONFLICT ("group", "key") WHERE account_id IS NULL
		 DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
	)
	s.exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_security', 'session_idle_minutes', '45', NOW(), NOW())`,
		accountID,
	)
	s.exec(
		`INSERT INTO account_activity
			(id, account_id, actor_user_id, action, target_type, target_id, metadata, created_at)
		 VALUES (?, ?, ?, 'member.role_changed', 'account_user', ?, '{"role":"admin"}', NOW())`,
		uuid.New(), accountID, admin.ID, accountID.String(),
	)

	pageKeys := s.platformPageKeys("mail")
	scanKey := "settings:platform:deposit_scan"
	accountKey := "settings:account:" + accountID.String() + ":account_security"
	s.seedCache(append(append([]string{}, pageKeys...), scanKey, accountKey)...)

	raw := s.reset(session.AccessToken, "mail", 200)
	s.NotContains(raw, platformResetSecret)
	s.NotContains(raw, platformResetStoredHost)
	s.NotContains(raw, "enc:v1:")
	var view struct {
		Name string `json:"name"`
	}
	s.Require().NoError(json.Unmarshal([]byte(raw), &view))
	s.Equal("mail", view.Name)

	for _, key := range pageKeys {
		s.False(facades.Cache().Has(key), key)
	}
	s.Equal("stale-"+scanKey, facades.Cache().GetString(scanKey))
	s.Equal("stale-"+accountKey, facades.Cache().GetString(accountKey))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp'`))
	s.Equal("80", s.storedPlatformValue("deposit_scan", "batch_blocks"))
	s.Equal("45", s.storedAccountValue(accountID, "account_security", "session_idle_minutes"))

	shown := s.get(session.AccessToken, "/v1/platform/settings/mail_smtp")
	shown.AssertOk()
	body, err := shown.Content()
	s.Require().NoError(err)
	s.NotContains(body, platformResetSecret)
	s.NotContains(body, platformResetStoredHost)
	s.NotContains(body, "enc:v1:")
	var group struct {
		Name   string `json:"name"`
		Fields []struct {
			Key    string `json:"key"`
			Secret bool   `json:"secret"`
			IsSet  bool   `json:"is_set"`
			Value  any    `json:"value"`
		} `json:"fields"`
	}
	s.Require().NoError(json.Unmarshal([]byte(body), &group))
	s.Equal("mail_smtp", group.Name)
	var sawPassword, sawPort, sawHost, sawEncryption bool
	for _, field := range group.Fields {
		switch field.Key {
		case "password":
			sawPassword = true
			s.True(field.Secret)
			s.False(field.IsSet)
			s.Nil(field.Value)
		case "port":
			sawPort = true
			s.False(field.IsSet)
			s.Equal(float64(587), field.Value)
		case "host":
			sawHost = true
			s.False(field.IsSet)
			s.Equal("", field.Value)
		case "encryption":
			sawEncryption = true
			s.False(field.IsSet)
			s.Equal("tls", field.Value)
		}
	}
	s.True(sawPassword)
	s.True(sawPort)
	s.True(sawHost)
	s.True(sawEncryption)

	mailGroups := len(pageKeys)
	s.Equal(int64(mailGroups), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.section_reset' AND account_id IS NULL AND target_id = 'mail'`,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.section_reset' AND account_id IS NOT NULL`,
	))
	var metas []struct {
		Metadata string `gorm:"column:metadata"`
	}
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT metadata::text AS metadata FROM account_activity WHERE action = 'settings.section_reset' AND account_id IS NULL`,
	).Scan(&metas))
	s.Len(metas, mailGroups)
	sawSMTP := false
	for _, row := range metas {
		meta := row.Metadata
		s.NotContains(meta, platformResetSecret)
		s.NotContains(meta, platformResetStoredHost)
		s.NotContains(meta, "enc:v1:")
		var decoded struct {
			Group  string   `json:"group"`
			Fields []string `json:"fields"`
		}
		s.Require().NoError(json.Unmarshal([]byte(meta), &decoded))
		var rawMeta map[string]json.RawMessage
		s.Require().NoError(json.Unmarshal([]byte(meta), &rawMeta))
		s.Len(rawMeta, 2)
		s.NotEmpty(decoded.Group)
		s.NotEmpty(decoded.Fields)
		if decoded.Group == "mail_smtp" {
			sawSMTP = true
			s.Contains(decoded.Fields, "host")
			s.Contains(decoded.Fields, "password")
			s.Contains(decoded.Fields, "port")
		}
	}
	s.True(sawSMTP)

	accountPage := s.activity(session.AccessToken, "/v1/accounts/"+accountID.String()+"/activity")
	s.Equal(int64(1), accountPage.Total)
	s.Equal("member.role_changed", accountPage.Data[0].Action)
	for _, row := range accountPage.Data {
		s.NotEqual("settings.section_reset", row.Action)
	}
	platformPage := s.activity(session.AccessToken, "/v1/platform/activity")
	s.GreaterOrEqual(platformPage.Total, int64(mailGroups))
	resets := 0
	for _, row := range platformPage.Data {
		if row.Action != "settings.section_reset" {
			continue
		}
		resets++
		s.Nil(row.AccountID)
		s.Equal("mail", row.TargetID)
		encoded, err := json.Marshal(row.Metadata)
		s.Require().NoError(err)
		s.NotContains(string(encoded), platformResetSecret)
		s.NotContains(string(encoded), platformResetStoredHost)
	}
	s.GreaterOrEqual(resets, mailGroups)

	var trails []struct {
		Scope      string `gorm:"column:scope"`
		Event      string `gorm:"column:event"`
		Properties string `gorm:"column:properties"`
	}
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT scope, event, properties::text AS properties
		 FROM activity_log
		 WHERE subject_type = 'setting'
		   AND properties->'old'->>'group' = 'mail_smtp'`,
	).Scan(&trails))
	s.NotEmpty(trails)
	var sawRemovedPassword, sawRemovedHost bool
	for _, row := range trails {
		s.Equal("", row.Scope)
		s.NotContains(row.Scope, accountID.String())
		if strings.Contains(row.Properties, platformResetSecret) || strings.Contains(row.Properties, "enc:v1:") {
			s.Fail("activity log stored a settings secret")
		}
		var props struct {
			Old map[string]any `json:"old"`
		}
		s.Require().NoError(json.Unmarshal([]byte(row.Properties), &props))
		switch props.Old["key"] {
		case "password":
			sawRemovedPassword = true
			s.Equal("deleted", row.Event)
			s.Equal(true, props.Old["valueSet"])
			_, hasValue := props.Old["value"]
			s.False(hasValue)
		case "host":
			sawRemovedHost = true
			s.Equal(platformResetStoredHost, props.Old["value"])
		}
	}
	s.True(sawRemovedPassword)
	s.True(sawRemovedHost)
}

func (s *PlatformSettingsSectionResetTestSuite) TestUnknown_Section_IsNotFound() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	key := "settings:platform:mail_smtp"
	s.seedCache(key)

	response := s.resetParsed(session.AccessToken, "not-a-section", 404)
	s.Equal(responses.CodeNotFound, response["error"].(map[string]any)["code"])
	s.Equal("settings section not found", response["error"].(map[string]any)["message"])
	s.Equal("stale-"+key, facades.Cache().GetString(key))

	accountPage := s.resetParsed(session.AccessToken, "security", 404)
	s.Equal(responses.CodeNotFound, accountPage["error"].(map[string]any)["code"])
	s.Equal("stale-"+key, facades.Cache().GetString(key))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.section_reset'`))
}

func (s *PlatformSettingsSectionResetTestSuite) TestNon_Admin_OnAKnownSectionIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	key := "settings:platform:mail_smtp"
	s.seedCache(key)
	before := s.count(`SELECT count(*) FROM account_activity`)

	response := s.resetParsed(session.AccessToken, "mail", 403)
	s.Equal(responses.CodeForbidden, response["error"].(map[string]any)["code"])
	s.Equal("you do not have permission to update settings", response["error"].(map[string]any)["message"])
	s.Equal("stale-"+key, facades.Cache().GetString(key))
	s.Equal(before, s.count(`SELECT count(*) FROM account_activity`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp' AND "key" = 'host' AND value = ?`, platformResetStoredHost))
}

func (s *PlatformSettingsSectionResetTestSuite) TestNon_Admin_OnAnUnknownSectionIsNotFound() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	key := "settings:platform:mail_smtp"
	s.seedCache(key)

	response := s.resetParsed(session.AccessToken, "not-a-section", 404)
	s.Equal(responses.CodeNotFound, response["error"].(map[string]any)["code"])
	s.Equal("settings section not found", response["error"].(map[string]any)["message"])
	s.Equal("stale-"+key, facades.Cache().GetString(key))

	missing, err := s.Http(s.T()).Post("/v1/platform/settings/sections/mail/reset", strings.NewReader("{}"))
	s.Require().NoError(err)
	missing.AssertUnauthorized()
}

func (s *PlatformSettingsSectionResetTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	s.exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
}

func (s *PlatformSettingsSectionResetTestSuite) ownAccount(userID uuid.UUID) uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID:          accountID,
		Name:        "Platform reset " + accountID.String()[:8],
		Status:      "active",
		Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active",
	}))
	return accountID
}

func (s *PlatformSettingsSectionResetTestSuite) platformPageKeys(section string) []string {
	s.T().Helper()
	groups := settings.GroupsInSection(section)
	s.Require().NotEmpty(groups)
	keys := make([]string, 0, len(groups))
	for _, group := range groups {
		s.Equal(settings.ScopePlatform, group.Scope)
		keys = append(keys, "settings:platform:"+group.Name)
	}
	return keys
}

func (s *PlatformSettingsSectionResetTestSuite) seedCache(keys ...string) {
	s.T().Helper()
	s.T().Cleanup(func() {
		for _, key := range keys {
			facades.Cache().Forget(key)
		}
	})
	for _, key := range keys {
		s.Require().NoError(facades.Cache().Put(key, "stale-"+key, 10*time.Minute))
		s.True(facades.Cache().Has(key), key)
	}
}

func (s *PlatformSettingsSectionResetTestSuite) reset(token, section string, status int) string {
	s.T().Helper()
	resp := s.postSection(token, section)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}

func (s *PlatformSettingsSectionResetTestSuite) resetParsed(token, section string, status int) map[string]any {
	s.T().Helper()
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(s.reset(token, section, status)), &parsed))
	return parsed
}

func (s *PlatformSettingsSectionResetTestSuite) postSection(token, section string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Post("/v1/platform/settings/sections/"+section+"/reset", strings.NewReader("{}"))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformSettingsSectionResetTestSuite) get(token, path string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get(path)
	s.Require().NoError(err)
	return resp
}

type platformResetActivityPage struct {
	Data []struct {
		AccountID *string        `json:"account_id"`
		Action    string         `json:"action"`
		TargetID  string         `json:"target_id"`
		Metadata  map[string]any `json:"metadata"`
	} `json:"data"`
	Total int64 `json:"total"`
}

func (s *PlatformSettingsSectionResetTestSuite) activity(token, path string) platformResetActivityPage {
	s.T().Helper()
	resp := s.get(token, path)
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var page platformResetActivityPage
	s.Require().NoError(json.Unmarshal([]byte(content), &page))
	return page
}

func (s *PlatformSettingsSectionResetTestSuite) exec(query string, args ...any) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(query, args...)
	s.Require().NoError(err)
}

func (s *PlatformSettingsSectionResetTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformSettingsSectionResetTestSuite) storedPlatformValue(group, key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = ? AND "key" = ?`,
		group, key,
	).Scan(&value))
	return value
}

func (s *PlatformSettingsSectionResetTestSuite) storedAccountValue(accountID uuid.UUID, group, key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = ? AND "key" = ?`,
		accountID, group, key,
	).Scan(&value))
	return value
}
