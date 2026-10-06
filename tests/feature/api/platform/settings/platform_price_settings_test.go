package settings

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const priceCoinAPIKeyFixture = "price-coinapi-key-fixture"

// PlatformPriceSettingsTestSuite is PUT /v1/platform/settings for the S1.4.4
// price groups. A platform_admins row is the gate. The API key is sealed and
// stays out of the response and the activity row. Quotes read the sealed key
// through price.SettingsSource at request time.
type PlatformPriceSettingsTestSuite struct {
	authSuite
}

func TestPlatform_Price_SettingsSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformPriceSettingsTestSuite))
}

func (s *PlatformPriceSettingsTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	for _, group := range []string{"price_lookup", "price_coingecko", "price_coinmarketcap", "price_coinapi"} {
		settings.FacadeCache{}.Forget("settings:platform:" + group)
		_, err := facades.Orm().Query().Exec(
			`DELETE FROM settings WHERE account_id IS NULL AND "group" = ?`,
			group,
		)
		s.Require().NoError(err)
	}
}

func (s *PlatformPriceSettingsTestSuite) TestA_Platform_AdminStoresTheOrderAndOneProvider() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	order := s.putRaw(session.AccessToken, "/v1/platform/settings/price_lookup", priceJSON(map[string]any{
		"provider_order": []string{"coingecko", "coinmarketcap", "coinapi"},
	}))
	order.AssertOk()
	s.Equal("coingecko,coinmarketcap,coinapi", s.settingValue("price_lookup", "provider_order"))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = 'price_lookup'
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = ?::jsonb`,
		admin.ID, `{"fields":["provider_order"],"group":"price_lookup"}`,
	))

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/price_coinapi", priceJSON(map[string]any{
		"enabled": true,
		"api_key": priceCoinAPIKeyFixture,
	}))
	saved.AssertOk()
	content, err := saved.Content()
	s.Require().NoError(err)
	if responseIncludesSecret(content, []string{priceCoinAPIKeyFixture}) {
		s.Fail("the response included a price key")
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
	s.Equal("price_coinapi", view.Name)
	seen := map[string]struct {
		Key    string `json:"key"`
		Secret bool   `json:"secret"`
		IsSet  bool   `json:"is_set"`
		Value  any    `json:"value"`
	}{}
	for _, field := range view.Fields {
		seen[field.Key] = field
	}
	keyField, ok := seen["api_key"]
	s.Require().True(ok)
	s.True(keyField.Secret)
	s.True(keyField.IsSet)
	s.Nil(keyField.Value)
	s.Equal(true, seen["enabled"].Value)
	sealed := s.settingValue("price_coinapi", "api_key")
	if !strings.HasPrefix(sealed, "enc:v1:") || strings.Contains(sealed, priceCoinAPIKeyFixture) {
		s.Fail("the key was not sealed")
		return
	}
	s.Equal("true", s.settingValue("price_coinapi", "enabled"))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = 'price_coinapi'
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = ?::jsonb`,
		admin.ID, `{"fields":["api_key","enabled"],"group":"price_coinapi"}`,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE metadata::text LIKE '%enc:v1:%'`))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE metadata::text LIKE ?`,
		"%"+priceCoinAPIKeyFixture+"%",
	))

	blank := s.putRaw(session.AccessToken, "/v1/platform/settings/price_coinapi", priceJSON(map[string]any{
		"api_key": "",
	}))
	blank.AssertOk()
	if s.settingValue("price_coinapi", "api_key") != sealed {
		s.Fail("a blank key wiped the stored key")
	}
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = 'price_coinapi'`,
	))
}

func (s *PlatformPriceSettingsTestSuite) TestAn_Unknown_ProviderIsNotStored() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	refused := s.putRaw(session.AccessToken, "/v1/platform/settings/price_lookup", priceJSON(map[string]any{
		"provider_order": []string{"coingecko", "kraken"},
	}))
	refused.AssertUnprocessableEntity()
	content, err := refused.Content()
	s.Require().NoError(err)
	if !strings.Contains(content, "must be one of") {
		s.Fail("an unknown provider was not rejected")
	}
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'price_lookup'`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = 'price_lookup'`))

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/price_lookup", priceJSON(map[string]any{
		"provider_order": []string{"coinapi"},
	}))
	saved.AssertOk()
	again := s.putRaw(session.AccessToken, "/v1/platform/settings/price_lookup", priceJSON(map[string]any{
		"provider_order": []string{"not-a-provider"},
	}))
	again.AssertUnprocessableEntity()
	s.Equal("coinapi", s.settingValue("price_lookup", "provider_order"))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = 'price_lookup'`,
	))
}

func (s *PlatformPriceSettingsTestSuite) TestA_Non_AdminIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	for _, path := range []string{"/v1/platform/settings/price_lookup", "/v1/platform/settings/price_coinapi"} {
		body := priceJSON(map[string]any{"provider_order": []string{"coinapi"}})
		if strings.HasSuffix(path, "price_coinapi") {
			body = priceJSON(map[string]any{"enabled": true, "api_key": priceCoinAPIKeyFixture})
		}
		forbidden := s.putRaw(session.AccessToken, path, body)
		forbidden.AssertForbidden()
		content, err := forbidden.Content()
		s.Require().NoError(err)
		if responseIncludesSecret(content, []string{priceCoinAPIKeyFixture}) {
			s.Fail("the forbidden response included a price key")
			return
		}
		if !strings.Contains(content, "you do not have permission to update settings") {
			s.Fail("a non-admin was not forbidden")
			return
		}
	}
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" IN ('price_lookup', 'price_coinapi')`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
}

func (s *PlatformPriceSettingsTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformPriceSettingsTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp := s.Put(path, support.Session{AccessToken: token}, body)
	return resp
}

func (s *PlatformPriceSettingsTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformPriceSettingsTestSuite) settingValue(group, key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = ? AND "key" = ?`,
		group, key,
	).Scan(&value))
	return value
}

func priceJSON(body map[string]any) string {
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
