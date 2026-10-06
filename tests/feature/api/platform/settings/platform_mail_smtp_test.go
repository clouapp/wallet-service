package settings

import (
	"context"
	"crypto/subtle"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/container"
	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/mails"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const mailSMTPFixture = "mailbox-secret-value"

// PlatformMailSMTPTestSuite is PUT /v1/platform/settings/mail_smtp from S1.4.4.
// A platform_admins row is the gate. The password is sealed and stays out of
// the response and the activity row.
type PlatformMailSMTPTestSuite struct {
	authSuite
}

func TestPlatformMailSMTPSuite(t *testing.T) {
	suite.Run(t, new(PlatformMailSMTPTestSuite))
}

func (s *PlatformMailSMTPTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	settings.FacadeCache{}.Forget("settings:platform:mail_smtp")
	_, err := facades.Orm().Query().Exec(`DELETE FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp'`)
	s.Require().NoError(err)
	appfacades.RestoreMailBaseline()
}

func (s *PlatformMailSMTPTestSuite) TearDownTest() {
	appfacades.SetMailSendObserver(nil)
	appfacades.RestoreMailBaseline()
}

func (s *PlatformMailSMTPTestSuite) TestAPlatformAdminStoresASealedPasswordTheMailerReads() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	envHost := appfacades.Config().GetString("mail.mailers.smtp.host")
	envPassword := appfacades.Config().GetString("mail.mailers.smtp.password")

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_smtp",
		`{"host":"127.0.0.1","port":1,"encryption":"starttls","username":"mailer","password":"`+mailSMTPFixture+`"}`)
	saved.AssertOk()
	content, err := saved.Content()
	s.Require().NoError(err)
	if strings.Contains(content, mailSMTPFixture) || strings.Contains(content, "enc:v1:") {
		s.Fail("the response included the mail password")
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
	s.Equal("mail_smtp", view.Name)
	var passwordSeen bool
	for _, field := range view.Fields {
		if field.Key != "password" {
			continue
		}
		passwordSeen = true
		s.True(field.Secret)
		s.True(field.IsSet)
		s.Nil(field.Value)
	}
	s.True(passwordSeen)
	sealed := s.mailValue("password")
	if !strings.HasPrefix(sealed, "enc:v1:") || strings.Contains(sealed, mailSMTPFixture) {
		s.Fail("the password was not sealed")
	}
	s.Equal("127.0.0.1", s.mailValue("host"))
	s.Equal("1", s.mailValue("port"))
	s.Equal("starttls", s.mailValue("encryption"))
	s.Equal("mailer", s.mailValue("username"))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = 'mail_smtp'
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = '{"fields":["encryption","host","password","port","username"],"group":"mail_smtp"}'::jsonb`,
		admin.ID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE metadata::text LIKE ?`,
		"%"+mailSMTPFixture+"%",
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE metadata::text LIKE '%enc:v1:%'`))

	opened, err := container.MustMake[*settings.Service]().EffectiveMailSMTP(context.Background())
	s.Require().NoError(err)
	if !opened.UsePassword || subtle.ConstantTimeCompare([]byte(opened.Password), []byte(mailSMTPFixture)) != 1 {
		s.Fail("the mailer did not open the sealed password")
	}

	var seen appfacades.MailDial
	appfacades.SetMailSendObserver(func() {
		seen = appfacades.MailDial{
			Host:       appfacades.Config().GetString("mail.host"),
			Port:       appfacades.Config().GetInt("mail.port"),
			Encryption: appfacades.Config().GetString("mail.encryption"),
			Username:   appfacades.Config().GetString("mail.username"),
			Password:   appfacades.Config().GetString("mail.password"),
		}
	})
	_ = appfacades.Mail().To([]string{"nobody@example.test"}).Send(&mails.WelcomeMail{To: "nobody@example.test"})
	if seen.Host != "127.0.0.1" || seen.Port != 1 || seen.Encryption != "starttls" || seen.Username != "mailer" ||
		subtle.ConstantTimeCompare([]byte(seen.Password), []byte(mailSMTPFixture)) != 1 {
		s.Fail("the mailer did not read mail_smtp at send time")
	}
	if subtle.ConstantTimeCompare([]byte(appfacades.Config().GetString("mail.password")), []byte(envPassword)) != 1 {
		s.Fail("the password stayed in the mailer config after send")
	}
	if appfacades.Config().GetString("mail.host") != "" && appfacades.Config().GetString("mail.host") != envHost {
		s.Fail("the env mail host was not restored after send")
	}

	blank := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_smtp", `{"password":""}`)
	blank.AssertOk()
	if s.mailValue("password") != sealed {
		s.Fail("a blank password wiped the stored secret")
	}
	s.Equal(int64(1), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = 'mail_smtp'`))

	moved := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_smtp", `{"host":"127.0.0.2"}`)
	moved.AssertUnprocessableEntity()
	s.Equal("127.0.0.1", s.mailValue("host"))
	if s.mailValue("password") != sealed {
		s.Fail("a destination change without the password replaced the secret")
	}
}

func (s *PlatformMailSMTPTestSuite) TestAMissingRowKeepsTheEnvMailer() {
	envHost := appfacades.Config().GetString("mail.mailers.smtp.host")
	envPort := appfacades.Config().GetInt("mail.mailers.smtp.port")
	envEncryption := appfacades.Config().GetString("mail.mailers.smtp.encryption")
	envPassword := appfacades.Config().GetString("mail.mailers.smtp.password")

	var seen appfacades.MailDial
	appfacades.SetMailSendObserver(func() {
		smtp := mailSMTPMap(appfacades.Config().Get("mail"))
		seen = appfacades.MailDial{
			Host:       smtpString(smtp, "host"),
			Port:       smtpInt(smtp, "port"),
			Encryption: smtpString(smtp, "encryption"),
			Password:   smtpString(smtp, "password"),
		}
	})
	_ = appfacades.Mail().To([]string{"nobody@example.test"}).Send(&mails.WelcomeMail{To: "nobody@example.test"})
	if seen.Host != envHost || seen.Port != envPort || seen.Encryption != envEncryption ||
		subtle.ConstantTimeCompare([]byte(seen.Password), []byte(envPassword)) != 1 {
		s.Fail("a missing mail_smtp row replaced the env mailer")
	}
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp'`))
}

func (s *PlatformMailSMTPTestSuite) TestAFailedReadKeepsTheEnvMailer() {
	envHost := appfacades.Config().GetString("mail.mailers.smtp.host")
	previous := appfacades.SetMailSMTPReader(func(context.Context) (appfacades.MailDial, error) {
		return appfacades.MailDial{}, errMailReadFailed
	})
	defer appfacades.SetMailSMTPReader(previous)

	var sawHost string
	appfacades.SetMailSendObserver(func() {
		sawHost = smtpString(mailSMTPMap(appfacades.Config().Get("mail")), "host")
	})
	_ = appfacades.Mail().To([]string{"nobody@example.test"}).Send(&mails.WelcomeMail{To: "nobody@example.test"})
	if sawHost != envHost {
		s.Fail("a failed mail_smtp read replaced the env mailer")
	}
}

func (s *PlatformMailSMTPTestSuite) TestANonAdminIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	forbidden := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_smtp",
		`{"host":"127.0.0.1","port":1,"encryption":"starttls","username":"mailer","password":"`+mailSMTPFixture+`"}`)
	forbidden.AssertForbidden()
	content, err := forbidden.Content()
	s.Require().NoError(err)
	if strings.Contains(content, mailSMTPFixture) || strings.Contains(content, "enc:v1:") {
		s.Fail("the forbidden response included the mail password")
	}
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp'`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
}

func (s *PlatformMailSMTPTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformMailSMTPTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Put(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformMailSMTPTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformMailSMTPTestSuite) mailValue(key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp' AND "key" = ?`,
		key,
	).Scan(&value))
	return value
}

var errMailReadFailed = errString("db down")

type errString string

func (e errString) Error() string { return string(e) }

func mailSMTPMap(value any) map[string]any {
	root, _ := value.(map[string]any)
	mailers, _ := root["mailers"].(map[string]any)
	smtp, _ := mailers["smtp"].(map[string]any)
	return smtp
}

func smtpString(smtp map[string]any, key string) string {
	text, _ := smtp[key].(string)
	return text
}

func smtpInt(smtp map[string]any, key string) int {
	switch typed := smtp[key].(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
