package settings

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const platformFlushStoredHost = "smtp.example.test"

// PlatformSettingsSectionCacheTestSuite is POST /v1/platform/settings/sections/{section}/cache
// from S1.4.6. settings.update is not in a platform catalog, so a platform_admins
// row is the gate. An unknown section is 404 before that 403.
type PlatformSettingsSectionCacheTestSuite struct {
	authSuite
}

func TestPlatform_Settings_SectionCacheSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformSettingsSectionCacheTestSuite))
}

func (s *PlatformSettingsSectionCacheTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformSettingsSectionCacheTestSuite) TestAdmin_Flush_DeletesPlatformKeysAndLeavesStoredRows() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, 'mail_smtp', 'host', ?, NOW(), NOW())`,
		platformFlushStoredHost,
	)
	s.Require().NoError(err)

	pageKeys := s.platformPageKeys("mail")
	scanKey := "settings:platform:deposit_scan"
	accountKey := "settings:account:" + uuid.NewString() + ":account_security"
	s.seedCache(append(append([]string{}, pageKeys...), scanKey, accountKey)...)

	before := s.count(`SELECT count(*) FROM account_activity`)
	body := s.flush(session.AccessToken, "mail", 204)
	s.Empty(strings.TrimSpace(body))
	for _, key := range pageKeys {
		s.False(facades.Cache().Has(key), key)
	}
	s.Equal("stale-"+scanKey, facades.Cache().GetString(scanKey))
	s.Equal("stale-"+accountKey, facades.Cache().GetString(accountKey))
	s.Equal(platformFlushStoredHost, s.storedPlatformValue("mail_smtp", "host"))
	s.Equal(before, s.count(`SELECT count(*) FROM account_activity`))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp' AND "key" = 'host'`,
	))
}

func (s *PlatformSettingsSectionCacheTestSuite) TestUnknown_Section_IsNotFound() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	key := "settings:platform:mail_smtp"
	s.seedCache(key)

	response := s.flushParsed(session.AccessToken, "not-a-section", 404)
	s.Equal(responses.CodeNotFound, response["error"].(map[string]any)["code"])
	s.Equal("settings section not found", response["error"].(map[string]any)["message"])
	s.Equal("stale-"+key, facades.Cache().GetString(key))

	accountPage := s.flushParsed(session.AccessToken, "security", 404)
	s.Equal(responses.CodeNotFound, accountPage["error"].(map[string]any)["code"])
	s.Equal("stale-"+key, facades.Cache().GetString(key))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
}

func (s *PlatformSettingsSectionCacheTestSuite) TestNon_Admin_OnAKnownSectionIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	key := "settings:platform:mail_smtp"
	s.seedCache(key)
	before := s.count(`SELECT count(*) FROM account_activity`)

	response := s.flushParsed(session.AccessToken, "mail", 403)
	s.Equal(responses.CodeForbidden, response["error"].(map[string]any)["code"])
	s.Equal("you do not have permission to update settings", response["error"].(map[string]any)["message"])
	s.Equal("stale-"+key, facades.Cache().GetString(key))
	s.Equal(before, s.count(`SELECT count(*) FROM account_activity`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp'`))
}

func (s *PlatformSettingsSectionCacheTestSuite) TestNon_Admin_OnAnUnknownSectionIsNotFound() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	key := "settings:platform:mail_smtp"
	s.seedCache(key)

	response := s.flushParsed(session.AccessToken, "not-a-section", 404)
	s.Equal(responses.CodeNotFound, response["error"].(map[string]any)["code"])
	s.Equal("settings section not found", response["error"].(map[string]any)["message"])
	s.Equal("stale-"+key, facades.Cache().GetString(key))

	missing := s.Post("/v1/platform/settings/sections/mail/cache", support.Session{}, "{}")
	missing.AssertUnauthorized()
}

func (s *PlatformSettingsSectionCacheTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformSettingsSectionCacheTestSuite) platformPageKeys(section string) []string {
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

func (s *PlatformSettingsSectionCacheTestSuite) seedCache(keys ...string) {
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

func (s *PlatformSettingsSectionCacheTestSuite) flush(token, section string, status int) string {
	s.T().Helper()
	resp := s.postSection(token, section)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}

func (s *PlatformSettingsSectionCacheTestSuite) flushParsed(token, section string, status int) map[string]any {
	s.T().Helper()
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(s.flush(token, section, status)), &parsed))
	return parsed
}

func (s *PlatformSettingsSectionCacheTestSuite) postSection(token, section string) contractstesting.Response {
	s.T().Helper()
	resp := s.Post("/v1/platform/settings/sections/"+section+"/cache", support.Session{AccessToken: token}, "{}")
	return resp
}

func (s *PlatformSettingsSectionCacheTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformSettingsSectionCacheTestSuite) storedPlatformValue(group, key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = ? AND "key" = ?`,
		group, key,
	).Scan(&value))
	return value
}
