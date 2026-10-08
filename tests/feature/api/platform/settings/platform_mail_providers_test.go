package settings

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const (
	mailSESKeyFixture    = "ses-access-key-fixture"
	mailSESSecretFixture = "ses-secret-fixture"
	mailMailgunFixture   = "mailgun-secret-fixture"
	mailResendFixture    = "resend-api-key-fixture"
	mailPostmarkFixture  = "postmark-token-fixture"
)

// PlatformMailProvidersTestSuite is PUT /v1/platform/settings for the S1.4.4
// provider groups. A platform_admins row is the gate. Secrets are sealed and
// stay out of the response and the activity row. The mailer stays on SMTP.
type PlatformMailProvidersTestSuite struct {
	authSuite
}

func TestPlatform_Mail_ProvidersSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformMailProvidersTestSuite))
}

func (s *PlatformMailProvidersTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	settings.FacadeCache{}.Forget("settings:platform:mail_smtp")
	settings.FacadeCache{}.Forget("settings:platform:mail_delivery")
	for _, group := range mailProviderGroupNames() {
		settings.FacadeCache{}.Forget("settings:platform:" + group)
		_, err := facades.Orm().Query().Exec(
			`DELETE FROM settings WHERE account_id IS NULL AND "group" = ?`,
			group,
		)
		s.Require().NoError(err)
	}
}

func (s *PlatformMailProvidersTestSuite) TearDownTest() {
}

func (s *PlatformMailProvidersTestSuite) TestA_Platform_AdminStoresEachProvider() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	for _, provider := range httpMailProviders() {
		body := mailProviderJSON(provider.body)
		saved := s.putRaw(session.AccessToken, "/v1/platform/settings/"+provider.group, body)
		if !saved.IsSuccessful() {
			s.Fail("provider save was not accepted")
			return
		}
		content, err := saved.Content()
		s.Require().NoError(err)
		if responseIncludesSecret(content, provider.fixtures) {
			s.Fail("the response included a mail secret")
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
		for _, key := range provider.secrets {
			field, ok := seen[key]
			s.Require().True(ok)
			s.True(field.Secret)
			s.True(field.IsSet)
			s.Nil(field.Value)
			sealed := s.settingValue(provider.group, key)
			if !strings.HasPrefix(sealed, "enc:v1:") || containsAny(sealed, provider.fixtures) {
				s.Fail("the secret was not sealed")
				return
			}
		}
		for key, want := range provider.public {
			s.Equal(want, s.settingValue(provider.group, key))
			s.Equal(want, seen[key].Value)
		}
		s.Equal(int64(1), s.count(
			`SELECT count(*) FROM account_activity
			 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = ?
			   AND account_id IS NULL AND actor_user_id = ?
			   AND metadata = ?::jsonb`,
			provider.group, admin.ID, provider.activity,
		))
		s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE metadata::text LIKE '%enc:v1:%'`))
		for _, fixture := range provider.fixtures {
			s.Equal(int64(0), s.count(
				`SELECT count(*) FROM account_activity WHERE metadata::text LIKE ?`,
				"%"+fixture+"%",
			))
		}

		blankBody := map[string]any{}
		for _, key := range provider.secrets {
			blankBody[key] = ""
		}
		sealedBefore := map[string]string{}
		for _, key := range provider.secrets {
			sealedBefore[key] = s.settingValue(provider.group, key)
		}
		blank := s.putRaw(session.AccessToken, "/v1/platform/settings/"+provider.group, mailProviderJSON(blankBody))
		if !blank.IsSuccessful() {
			s.Fail("a blank secret was refused")
			return
		}
		for _, key := range provider.secrets {
			if s.settingValue(provider.group, key) != sealedBefore[key] {
				s.Fail("a blank secret wiped the stored secret")
				return
			}
		}
		s.Equal(int64(1), s.count(
			`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = ?`,
			provider.group,
		))
	}
}

func (s *PlatformMailProvidersTestSuite) TestAn_Incomplete_SESPairIsNotStored() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	refused := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_ses", mailProviderJSON(map[string]any{
		"key":    mailSESKeyFixture,
		"region": "us-east-1",
	}))
	if refused.IsSuccessful() {
		s.Fail("a half SES pair was accepted")
		return
	}
	content, err := refused.Content()
	s.Require().NoError(err)
	if responseIncludesSecret(content, []string{mailSESKeyFixture}) {
		s.Fail("the response included a mail secret")
		return
	}
	if !strings.Contains(content, "must be set together") {
		s.Fail("a half SES pair was not rejected")
		return
	}
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_ses'`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = 'mail_ses'`))
}

func (s *PlatformMailProvidersTestSuite) TestThe_Mailer_StaysOnSMTP() {
	envHost := appfacades.Config().GetString("mail.mailers.smtp.host")
	s.assertSMTPHost(envHost)

	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	refused := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_ses", mailProviderJSON(map[string]any{
		"key": mailSESKeyFixture,
	}))
	if refused.IsSuccessful() {
		s.Fail("a half SES pair was accepted")
		return
	}
	s.assertSMTPHost(envHost)

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_ses", mailProviderJSON(map[string]any{
		"key":          mailSESKeyFixture,
		"secret":       mailSESSecretFixture,
		"region":       "us-east-1",
		"from_address": "ses-from@example.test",
		"from_name":    "SES",
	}))
	if !saved.IsSuccessful() {
		s.Fail("provider save was not accepted")
		return
	}
	s.assertSMTPHost(envHost)
}

func (s *PlatformMailProvidersTestSuite) TestA_Non_AdminIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	for _, provider := range httpMailProviders() {
		forbidden := s.putRaw(session.AccessToken, "/v1/platform/settings/"+provider.group, mailProviderJSON(provider.body))
		content, err := forbidden.Content()
		s.Require().NoError(err)
		if responseIncludesSecret(content, provider.fixtures) {
			s.Fail("the forbidden response included a mail secret")
			return
		}
		if !strings.Contains(content, "you do not have permission to update settings") {
			s.Fail("a non-admin was not forbidden")
			return
		}
		s.Equal(int64(0), s.count(
			`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = ?`,
			provider.group,
		))
	}
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
}

func (s *PlatformMailProvidersTestSuite) assertSMTPHost(envHost string) {
	s.T().Helper()
	var seen string
	_ = sendWelcomeObserved(func() {
		seen = smtpString(mailSMTPMap(appfacades.Config().Get("mail")), "host")
	})
	if seen != envHost {
		s.Fail("the mailer left SMTP")
	}
}

func (s *PlatformMailProvidersTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformMailProvidersTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp := s.Put(path, support.Session{AccessToken: token}, body)
	return resp
}

func (s *PlatformMailProvidersTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformMailProvidersTestSuite) settingValue(group, key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = ? AND "key" = ?`,
		group, key,
	).Scan(&value))
	return value
}

type httpMailProvider struct {
	group    string
	body     map[string]any
	secrets  []string
	fixtures []string
	public   map[string]string
	activity string
}

func httpMailProviders() []httpMailProvider {
	return []httpMailProvider{
		{
			group: "mail_ses",
			body: map[string]any{
				"key":          mailSESKeyFixture,
				"secret":       mailSESSecretFixture,
				"region":       "us-east-1",
				"from_address": "ses-from@example.test",
				"from_name":    "SES",
			},
			secrets:  []string{"key", "secret"},
			fixtures: []string{mailSESKeyFixture, mailSESSecretFixture},
			public: map[string]string{
				"region":       "us-east-1",
				"from_address": "ses-from@example.test",
				"from_name":    "SES",
			},
			activity: `{"fields":["from_address","from_name","key","region","secret"],"group":"mail_ses"}`,
		},
		{
			group: "mail_mailgun",
			body: map[string]any{
				"domain":       "mg.example.test",
				"secret":       mailMailgunFixture,
				"endpoint":     "api.mailgun.net",
				"from_address": "mg-from@example.test",
				"from_name":    "Mailgun",
			},
			secrets:  []string{"secret"},
			fixtures: []string{mailMailgunFixture},
			public: map[string]string{
				"domain":       "mg.example.test",
				"endpoint":     "api.mailgun.net",
				"from_address": "mg-from@example.test",
				"from_name":    "Mailgun",
			},
			activity: `{"fields":["domain","endpoint","from_address","from_name","secret"],"group":"mail_mailgun"}`,
		},
		{
			group: "mail_resend",
			body: map[string]any{
				"api_key":      mailResendFixture,
				"from_address": "resend-from@example.test",
				"from_name":    "Resend",
			},
			secrets:  []string{"api_key"},
			fixtures: []string{mailResendFixture},
			public: map[string]string{
				"from_address": "resend-from@example.test",
				"from_name":    "Resend",
			},
			activity: `{"fields":["api_key","from_address","from_name"],"group":"mail_resend"}`,
		},
		{
			group: "mail_postmark",
			body: map[string]any{
				"token":             mailPostmarkFixture,
				"message_stream_id": "outbound",
				"from_address":      "postmark-from@example.test",
				"from_name":         "Postmark",
			},
			secrets:  []string{"token"},
			fixtures: []string{mailPostmarkFixture},
			public: map[string]string{
				"message_stream_id": "outbound",
				"from_address":      "postmark-from@example.test",
				"from_name":         "Postmark",
			},
			activity: `{"fields":["from_address","from_name","message_stream_id","token"],"group":"mail_postmark"}`,
		},
	}
}

func mailProviderGroupNames() []string {
	names := make([]string, 0, len(httpMailProviders()))
	for _, provider := range httpMailProviders() {
		names = append(names, provider.group)
	}
	return names
}

func mailProviderJSON(body map[string]any) string {
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func responseIncludesSecret(content string, fixtures []string) bool {
	if strings.Contains(content, "enc:v1:") {
		return true
	}
	return containsAny(content, fixtures)
}

func containsAny(value string, parts []string) bool {
	for _, part := range parts {
		if part != "" && strings.Contains(value, part) {
			return true
		}
	}
	return false
}
