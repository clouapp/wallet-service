package settings

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/testutil"
)

// PlatformEtherscanSettingsTestSuite is PUT /v1/platform/settings/provider_etherscan.
// A platform_admins row is the gate. The api key is sealed and stays out of
// the response and the activity row. Block height stays on today's source.
type PlatformEtherscanSettingsTestSuite struct {
	authSuite
}

func TestPlatformEtherscanSettingsSuite(t *testing.T) {
	suite.Run(t, new(PlatformEtherscanSettingsTestSuite))
}

func (s *PlatformEtherscanSettingsTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	_, err := facades.Orm().Query().Exec(
		`DELETE FROM settings WHERE account_id IS NULL AND "group" = ?`,
		"provider_etherscan",
	)
	s.Require().NoError(err)
	settings.FacadeCache{}.Forget("settings:platform:provider_etherscan")
}

func (s *PlatformEtherscanSettingsTestSuite) TestAPlatformAdminStoresEnabledAndASealedKey() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	const fixture = "provider-etherscan-key-fixture"

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/provider_etherscan", providerJSON(map[string]any{
		"enabled": true,
		"api_key": fixture,
	}))
	saved.AssertOk()
	content, err := saved.Content()
	s.Require().NoError(err)
	if responseIncludesSecret(content, []string{fixture}) {
		s.Fail("the response included the api key")
		return
	}
	var view struct {
		Name   string `json:"name"`
		Fields []struct {
			Key    string `json:"key"`
			Secret bool   `json:"secret"`
			IsSet  bool   `json:"is_set"`
			Value  any    `json:"value"`
		} `json:"fields"`
	}
	s.decode(saved, &view)
	s.Equal("provider_etherscan", view.Name)
	seen := map[string]struct {
		Key    string `json:"key"`
		Secret bool   `json:"secret"`
		IsSet  bool   `json:"is_set"`
		Value  any    `json:"value"`
	}{}
	for _, field := range view.Fields {
		seen[field.Key] = field
	}
	secretField, ok := seen["api_key"]
	s.True(ok)
	s.True(secretField.Secret)
	s.True(secretField.IsSet)
	if secretField.Value != nil {
		s.Fail("the response included the api key")
		return
	}
	s.Equal(true, seen["enabled"].Value)
	sealed := s.settingValue("provider_etherscan", "api_key")
	if !strings.HasPrefix(sealed, "enc:v1:") || strings.Contains(sealed, fixture) {
		s.Fail("the api key was not sealed")
		return
	}
	s.Equal("true", s.settingValue("provider_etherscan", "enabled"))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = ?
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = ?::jsonb`,
		"provider_etherscan", admin.ID, `{"fields":["api_key","enabled"],"group":"provider_etherscan"}`,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE metadata::text LIKE '%enc:v1:%'`))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE metadata::text LIKE ?`,
		"%"+fixture+"%",
	))

	blank := s.putRaw(session.AccessToken, "/v1/platform/settings/provider_etherscan", providerJSON(map[string]any{
		"api_key": "",
	}))
	blank.AssertOk()
	if s.settingValue("provider_etherscan", "api_key") != sealed {
		s.Fail("a blank key wiped the stored key")
		return
	}
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = ?`,
		"provider_etherscan",
	))
}

func (s *PlatformEtherscanSettingsTestSuite) TestANonAdminIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	const fixture = "provider-etherscan-key-fixture"

	forbidden := s.putRaw(session.AccessToken, "/v1/platform/settings/provider_etherscan", providerJSON(map[string]any{
		"enabled": true,
		"api_key": fixture,
	}))
	forbidden.AssertForbidden()
	content, err := forbidden.Content()
	s.Require().NoError(err)
	if responseIncludesSecret(content, []string{fixture}) {
		s.Fail("the forbidden response included the api key")
		return
	}
	if !strings.Contains(content, "you do not have permission to update settings") {
		s.Fail("a non-admin was not forbidden")
		return
	}
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'provider_etherscan'`,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
}

func (s *PlatformEtherscanSettingsTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformEtherscanSettingsTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Put(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformEtherscanSettingsTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformEtherscanSettingsTestSuite) settingValue(group, key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = ? AND "key" = ?`,
		group, key,
	).Scan(&value))
	return value
}
