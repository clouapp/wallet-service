package settings

import (
	"encoding/json"
	"strings"
	"testing"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const platformGroupHTTPSecret = "platform-group-http-secret"

// PlatformSettingsGroupTestSuite is GET /v1/platform/settings/{group} from S1.4.6.
// settings.view plus the group's ViewPermission; a platform_admins row is the
// gate because those names are not in a platform catalog. An unknown group,
// including an account-only group, is 404 before that 403.
type PlatformSettingsGroupTestSuite struct {
	authSuite
}

func TestPlatformSettingsGroupSuite(t *testing.T) {
	suite.Run(t, new(PlatformSettingsGroupTestSuite))
}

func (s *PlatformSettingsGroupTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	settings.FacadeCache{}.Forget("settings:platform:mail_smtp")
}

func (s *PlatformSettingsGroupTestSuite) TestAPlatformAdminSeesAKnownGroupWithoutSecrets() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	empty := s.getRaw(session.AccessToken, "/v1/platform/settings/mail_smtp")
	empty.AssertOk()
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp'`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
	var defaults struct {
		Name   string `json:"name"`
		Scope  string `json:"scope"`
		Fields []struct {
			Key    string `json:"key"`
			Secret bool   `json:"secret"`
			IsSet  bool   `json:"is_set"`
			Value  any    `json:"value"`
		} `json:"fields"`
	}
	s.decode(empty, &defaults)
	s.Equal("mail_smtp", defaults.Name)
	s.Equal("platform", defaults.Scope)
	var sawPassword, sawPort bool
	for _, field := range defaults.Fields {
		if field.Key == "password" {
			sawPassword = true
			s.True(field.Secret)
			s.False(field.IsSet)
			s.Nil(field.Value)
		}
		if field.Key == "port" {
			sawPort = true
			s.False(field.Secret)
			s.Equal(float64(587), field.Value)
		}
	}
	s.True(sawPassword)
	s.True(sawPort)

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_smtp",
		`{"host":"127.0.0.1","port":1,"encryption":"starttls","username":"mailer","password":"`+platformGroupHTTPSecret+`"}`)
	saved.AssertOk()

	shown := s.getRaw(session.AccessToken, "/v1/platform/settings/mail_smtp")
	shown.AssertOk()
	raw, err := shown.Content()
	s.Require().NoError(err)
	if strings.Contains(raw, platformGroupHTTPSecret) || strings.Contains(raw, "enc:v1:") {
		s.Fail("the group included a secret")
	}
	var body struct {
		Name   string `json:"name"`
		Fields []struct {
			Key    string `json:"key"`
			Secret bool   `json:"secret"`
			IsSet  bool   `json:"is_set"`
			Value  any    `json:"value"`
		} `json:"fields"`
	}
	s.decode(shown, &body)
	s.Equal("mail_smtp", body.Name)
	var passwordSeen, hostSeen bool
	for _, field := range body.Fields {
		switch field.Key {
		case "password":
			passwordSeen = true
			s.True(field.Secret)
			s.True(field.IsSet)
			s.Nil(field.Value)
		case "host":
			hostSeen = true
			s.Equal("127.0.0.1", field.Value)
		}
	}
	s.True(passwordSeen)
	s.True(hostSeen)

	sealed := s.mailPassword()
	if !strings.HasPrefix(sealed, "enc:v1:") || strings.Contains(sealed, platformGroupHTTPSecret) {
		s.Fail("the password was not sealed")
	}
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = 'mail_smtp'`,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'platform.secret_viewed'`))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE metadata::text LIKE ?`,
		"%"+platformGroupHTTPSecret+"%",
	))

	missing := s.getRaw(session.AccessToken, "/v1/platform/settings/no-such-group")
	missing.AssertNotFound()
	accountOnly := s.getRaw(session.AccessToken, "/v1/platform/settings/account_security")
	accountOnly.AssertNotFound()
	var notFound struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(accountOnly, &notFound)
	s.Equal(responses.CodeNotFound, notFound.Error.Code)
	s.Equal("settings group not found", notFound.Error.Message)
}

func (s *PlatformSettingsGroupTestSuite) TestASecretSettingRecordsKeyAndValueSet() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	const secret = "activity-log-must-not-store-this"
	settings.FacadeCache{}.Forget("settings:platform:mail_smtp")

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_smtp",
		`{"host":"smtp.example","port":2525,"encryption":"starttls","username":"mailer","password":"`+secret+`"}`)
	saved.AssertOk()

	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM activity_log
		 WHERE subject_type = 'setting'
		   AND (properties::text LIKE '%' || ? || '%' OR properties::text LIKE '%enc:v1:%')`,
		secret,
	))

	var rows []struct {
		Scope      string `gorm:"column:scope"`
		Event      string `gorm:"column:event"`
		Properties string `gorm:"column:properties"`
	}
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT scope, event, properties::text AS properties
		 FROM activity_log
		 WHERE subject_type = 'setting' AND properties->'new'->>'group' = 'mail_smtp'`,
	).Scan(&rows))
	s.NotEmpty(rows)

	var sawPassword, sawHost bool
	for _, row := range rows {
		s.Equal("", row.Scope)
		if strings.Contains(row.Properties, secret) || strings.Contains(row.Properties, "enc:v1:") {
			s.Fail("activity log stored a settings secret")
		}
		var props struct {
			New map[string]any `json:"new"`
		}
		s.Require().NoError(json.Unmarshal([]byte(row.Properties), &props))
		switch props.New["key"] {
		case "password":
			sawPassword = true
			s.Equal(true, props.New["valueSet"])
			_, hasValue := props.New["value"]
			s.False(hasValue)
			s.Equal("created", row.Event)
			s.Equal("password", props.New["key"])
		case "host":
			sawHost = true
			s.Equal("smtp.example", props.New["value"])
		}
	}
	s.True(sawPassword)
	s.True(sawHost)
}

func (s *PlatformSettingsGroupTestSuite) TestANonAdminOnAnUnknownGroupIsNotFoundBeforeForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)

	unknown := s.getRaw(session.AccessToken, "/v1/platform/settings/no-such-group")
	unknown.AssertNotFound()
	accountOnly := s.getRaw(session.AccessToken, "/v1/platform/settings/account_security")
	accountOnly.AssertNotFound()

	known := s.getRaw(session.AccessToken, "/v1/platform/settings/mail_smtp")
	known.AssertForbidden()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(known, &body)
	s.Equal(responses.CodeForbidden, body.Error.Code)
	s.Equal("you do not have permission to view settings", body.Error.Message)
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp'`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'platform.secret_viewed'`))

	missing, err := s.Http(s.T()).Get("/v1/platform/settings/mail_smtp")
	s.Require().NoError(err)
	missing.AssertUnauthorized()
}

func (s *PlatformSettingsGroupTestSuite) TestAPlatformAdminGetsProviderCredentialGroupsWithoutTheSecret() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	const fixture = "provider-credential-http-secret"

	for _, group := range httpProviderCredentialGroups() {
		settings.FacadeCache{}.Forget("settings:platform:" + group.name)
		_, err := facades.Orm().Query().Exec(
			`DELETE FROM settings WHERE account_id IS NULL AND "group" = ?`,
			group.name,
		)
		s.Require().NoError(err)

		empty := s.getRaw(session.AccessToken, "/v1/platform/settings/"+group.name)
		empty.AssertOk()
		s.assertProviderSecretOmitted(empty, group.name, group.secretKey, false)

		saved := s.putRaw(session.AccessToken, "/v1/platform/settings/"+group.name, providerJSON(map[string]any{
			"enabled":       true,
			group.secretKey: fixture,
		}))
		saved.AssertOk()

		shown := s.getRaw(session.AccessToken, "/v1/platform/settings/"+group.name)
		shown.AssertOk()
		raw, err := shown.Content()
		s.Require().NoError(err)
		if strings.Contains(raw, fixture) || strings.Contains(raw, "enc:v1:") {
			s.Fail(group.name + " included a secret")
		}
		s.assertProviderSecretOmitted(shown, group.name, group.secretKey, true)
	}
}

func (s *PlatformSettingsGroupTestSuite) assertProviderSecretOmitted(response contractstesting.Response, groupName, secretKey string, isSet bool) {
	s.T().Helper()
	var body struct {
		Name   string `json:"name"`
		Fields []struct {
			Key    string `json:"key"`
			Secret bool   `json:"secret"`
			IsSet  bool   `json:"is_set"`
			Value  any    `json:"value"`
		} `json:"fields"`
	}
	s.decode(response, &body)
	s.Equal(groupName, body.Name)
	var seen bool
	for _, field := range body.Fields {
		if field.Key != secretKey {
			continue
		}
		seen = true
		s.True(field.Secret)
		s.Equal(isSet, field.IsSet)
		s.Nil(field.Value)
	}
	s.True(seen)
}

func httpProviderCredentialGroups() []struct {
	name      string
	secretKey string
} {
	return []struct {
		name      string
		secretKey string
	}{
		{name: "provider_alchemy", secretKey: "auth_token"},
		{name: "provider_helius", secretKey: "api_key"},
		{name: "provider_quicknode", secretKey: "api_key"},
		{name: "provider_etherscan", secretKey: "api_key"},
		{name: "price_coingecko", secretKey: "api_key"},
		{name: "price_coinmarketcap", secretKey: "api_key"},
		{name: "price_coinapi", secretKey: "api_key"},
	}
}

func (s *PlatformSettingsGroupTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformSettingsGroupTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Put(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformSettingsGroupTestSuite) getRaw(token, path string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get(path)
	s.Require().NoError(err)
	return resp
}

func (s *PlatformSettingsGroupTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformSettingsGroupTestSuite) mailPassword() string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp' AND "key" = 'password'`,
	).Scan(&value))
	return value
}
