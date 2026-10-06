package settings

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/testutil"
)

// PlatformProviderSettingsTestSuite is PUT /v1/platform/settings for the
// S1.4.4 webhook provider groups. A platform_admins row is the gate. The
// secret is sealed and stays out of the response and the activity row.
// Chain dialing stays on chains.rpc_url.
type PlatformProviderSettingsTestSuite struct {
	authSuite
}

func TestPlatformProviderSettingsSuite(t *testing.T) {
	suite.Run(t, new(PlatformProviderSettingsTestSuite))
}

func (s *PlatformProviderSettingsTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	for _, group := range []string{"provider_alchemy", "provider_helius", "provider_quicknode"} {
		settings.FacadeCache{}.Forget("settings:platform:" + group)
		_, err := facades.Orm().Query().Exec(
			`DELETE FROM settings WHERE account_id IS NULL AND "group" = ?`,
			group,
		)
		s.Require().NoError(err)
	}
}

func (s *PlatformProviderSettingsTestSuite) TestAPlatformAdminStoresEachProvider() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	for _, provider := range httpWebhookProviders() {
		body := map[string]any{"enabled": true}
		body[provider.secretKey] = provider.fixture
		saved := s.putRaw(session.AccessToken, "/v1/platform/settings/"+provider.group, providerJSON(body))
		saved.AssertOk()
		content, err := saved.Content()
		s.Require().NoError(err)
		if responseIncludesSecret(content, []string{provider.fixture}) {
			s.Fail("the response included a provider secret")
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
		s.Equal(provider.group, view.Name)
		seen := map[string]struct {
			Key    string `json:"key"`
			Secret bool   `json:"secret"`
			IsSet  bool   `json:"is_set"`
			Value  any    `json:"value"`
		}{}
		for _, field := range view.Fields {
			seen[field.Key] = field
		}
		secretField, ok := seen[provider.secretKey]
		s.True(ok)
		s.True(secretField.Secret)
		s.True(secretField.IsSet)
		if secretField.Value != nil {
			s.Fail("the response included a provider secret")
			return
		}
		s.Equal(true, seen["enabled"].Value)
		sealed := s.settingValue(provider.group, provider.secretKey)
		if !strings.HasPrefix(sealed, "enc:v1:") || strings.Contains(sealed, provider.fixture) {
			s.Fail("the secret was not sealed")
			return
		}
		s.Equal("true", s.settingValue(provider.group, "enabled"))
		s.Equal(int64(1), s.count(
			`SELECT count(*) FROM account_activity
			 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = ?
			   AND account_id IS NULL AND actor_user_id = ?
			   AND metadata = ?::jsonb`,
			provider.group, admin.ID, provider.activity,
		))
		s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE metadata::text LIKE '%enc:v1:%'`))
		s.Equal(int64(0), s.count(
			`SELECT count(*) FROM account_activity WHERE metadata::text LIKE ?`,
			"%"+provider.fixture+"%",
		))

		blankBody := map[string]any{}
		blankBody[provider.secretKey] = ""
		blank := s.putRaw(session.AccessToken, "/v1/platform/settings/"+provider.group, providerJSON(blankBody))
		blank.AssertOk()
		if s.settingValue(provider.group, provider.secretKey) != sealed {
			s.Fail("a blank secret wiped the stored secret")
			return
		}
		s.Equal(int64(1), s.count(
			`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = ?`,
			provider.group,
		))
	}
}

func (s *PlatformProviderSettingsTestSuite) TestANonAdminIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	for _, provider := range httpWebhookProviders() {
		body := map[string]any{"enabled": true}
		body[provider.secretKey] = provider.fixture
		forbidden := s.putRaw(session.AccessToken, "/v1/platform/settings/"+provider.group, providerJSON(body))
		forbidden.AssertForbidden()
		content, err := forbidden.Content()
		s.Require().NoError(err)
		if responseIncludesSecret(content, []string{provider.fixture}) {
			s.Fail("the forbidden response included a provider secret")
			return
		}
		if !strings.Contains(content, "you do not have permission to update settings") {
			s.Fail("a non-admin was not forbidden")
			return
		}
	}
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" IN ('provider_alchemy', 'provider_helius', 'provider_quicknode')`,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
}

func (s *PlatformProviderSettingsTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformProviderSettingsTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Put(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformProviderSettingsTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformProviderSettingsTestSuite) settingValue(group, key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = ? AND "key" = ?`,
		group, key,
	).Scan(&value))
	return value
}

type httpWebhookProvider struct {
	group     string
	secretKey string
	fixture   string
	activity  string
}

func httpWebhookProviders() []httpWebhookProvider {
	return []httpWebhookProvider{
		{
			group:     "provider_alchemy",
			secretKey: "auth_token",
			fixture:   "provider-alchemy-token-fixture",
			activity:  `{"fields":["auth_token","enabled"],"group":"provider_alchemy"}`,
		},
		{
			group:     "provider_helius",
			secretKey: "api_key",
			fixture:   "provider-helius-key-fixture",
			activity:  `{"fields":["api_key","enabled"],"group":"provider_helius"}`,
		},
		{
			group:     "provider_quicknode",
			secretKey: "api_key",
			fixture:   "provider-quicknode-key-fixture",
			activity:  `{"fields":["api_key","enabled"],"group":"provider_quicknode"}`,
		},
	}
}

func providerJSON(body map[string]any) string {
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
