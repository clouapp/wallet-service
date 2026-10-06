package settings

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/mails"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// PlatformMailDeliveryTestSuite is PUT /v1/platform/settings/mail_delivery
// from S1.4.4. A platform_admins row is the gate. from_address and from_name
// are stored in the clear and applied as the From header at send time.
type PlatformMailDeliveryTestSuite struct {
	authSuite
}

func TestPlatform_Mail_DeliverySuite(t *testing.T) {
	support.RunSuite(t, new(PlatformMailDeliveryTestSuite))
}

func (s *PlatformMailDeliveryTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	settings.FacadeCache{}.Forget("settings:platform:mail_delivery")
	_, err := facades.Orm().Query().Exec(`DELETE FROM settings WHERE account_id IS NULL AND "group" = 'mail_delivery'`)
	s.Require().NoError(err)
	appfacades.RestoreMailBaseline()
}

func (s *PlatformMailDeliveryTestSuite) TearDownTest() {
	appfacades.SetMailSendObserver(nil)
	appfacades.RestoreMailBaseline()
}

func (s *PlatformMailDeliveryTestSuite) TestA_Platform_AdminStoresTheFromHeaderTheMailerReads() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	envAddress := appfacades.Config().GetString("mail.from.address")
	envName := appfacades.Config().GetString("mail.from.name")

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_delivery",
		`{"driver":"smtp","from_address":"from-header@example.test","from_name":"Macro"}`)
	saved.AssertOk()
	content, err := saved.Content()
	s.Require().NoError(err)
	if strings.Contains(content, "enc:v1:") {
		s.Fail("the response sealed the from header")
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
	s.Equal("mail_delivery", view.Name)
	seen := map[string]any{}
	for _, field := range view.Fields {
		s.False(field.Secret)
		s.True(field.IsSet)
		seen[field.Key] = field.Value
	}
	s.Equal("smtp", seen["driver"])
	s.Equal("from-header@example.test", seen["from_address"])
	s.Equal("Macro", seen["from_name"])
	s.Equal("from-header@example.test", s.mailValue("from_address"))
	s.Equal("Macro", s.mailValue("from_name"))
	s.Equal("smtp", s.mailValue("driver"))
	if strings.HasPrefix(s.mailValue("from_address"), "enc:v1:") || strings.HasPrefix(s.mailValue("from_name"), "enc:v1:") {
		s.Fail("the from header was sealed")
	}
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = 'mail_delivery'
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = '{"fields":["driver","from_address","from_name"],"group":"mail_delivery"}'::jsonb`,
		admin.ID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE metadata::text LIKE ?`,
		"%from-header@example.test%",
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE metadata::text LIKE '%Macro%'`))

	var seenAddress, seenName string
	appfacades.SetMailSendObserver(func() {
		seenAddress = appfacades.Config().GetString("mail.from.address")
		seenName = appfacades.Config().GetString("mail.from.name")
	})
	_ = appfacades.Mail().To([]string{"nobody@example.test"}).Send(&mails.WelcomeMail{To: "nobody@example.test"})
	if seenAddress != "from-header@example.test" || seenName != "Macro" {
		s.Fail("the mailer did not read mail_delivery at send time")
	}
	if appfacades.Config().GetString("mail.from.address") != envAddress || appfacades.Config().GetString("mail.from.name") != envName {
		s.Fail("the env from header was not restored after send")
	}

	rejected := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_delivery", `{"from_address":"not-an-email"}`)
	rejected.AssertUnprocessableEntity()
	rejectedBody, err := rejected.Content()
	s.Require().NoError(err)
	s.Contains(rejectedBody, `"validation_failed"`)
	s.Equal("from-header@example.test", s.mailValue("from_address"))
	s.Equal("Macro", s.mailValue("from_name"))

	blankName := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_delivery", `{"from_name":""}`)
	blankName.AssertUnprocessableEntity()
	s.Equal("Macro", s.mailValue("from_name"))
	s.Equal("from-header@example.test", s.mailValue("from_address"))
	s.Equal(int64(1), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = 'mail_delivery'`))
}

func (s *PlatformMailDeliveryTestSuite) TestA_Missing_RowKeepsTheEnvFromHeader() {
	envAddress := appfacades.Config().GetString("mail.from.address")
	envName := appfacades.Config().GetString("mail.from.name")

	var seenAddress, seenName string
	appfacades.SetMailSendObserver(func() {
		seenAddress = appfacades.Config().GetString("mail.from.address")
		seenName = appfacades.Config().GetString("mail.from.name")
	})
	_ = appfacades.Mail().To([]string{"nobody@example.test"}).Send(&mails.WelcomeMail{To: "nobody@example.test"})
	if seenAddress != envAddress || seenName != envName {
		s.Fail("a missing mail_delivery row replaced the env from header")
	}
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_delivery'`))
}

func (s *PlatformMailDeliveryTestSuite) TestA_Failed_ReadKeepsTheEnvFromHeader() {
	envAddress := appfacades.Config().GetString("mail.from.address")
	envName := appfacades.Config().GetString("mail.from.name")
	previous := appfacades.SetMailFromReader(func(context.Context) (appfacades.MailFrom, error) {
		return appfacades.MailFrom{Address: "replaced@example.test", Name: "Replaced", UseAddress: true, UseName: true}, errMailReadFailed
	})
	defer appfacades.SetMailFromReader(previous)

	var seenAddress, seenName string
	appfacades.SetMailSendObserver(func() {
		seenAddress = appfacades.Config().GetString("mail.from.address")
		seenName = appfacades.Config().GetString("mail.from.name")
	})
	_ = appfacades.Mail().To([]string{"nobody@example.test"}).Send(&mails.WelcomeMail{To: "nobody@example.test"})
	if seenAddress != envAddress || seenName != envName {
		s.Fail("a failed mail_delivery read replaced the env from header")
	}
}

func (s *PlatformMailDeliveryTestSuite) TestA_Non_AdminIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	forbidden := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_delivery",
		`{"driver":"smtp","from_address":"from-header@example.test","from_name":"Macro"}`)
	forbidden.AssertForbidden()
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_delivery'`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
}

func (s *PlatformMailDeliveryTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformMailDeliveryTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp := s.Put(path, support.Session{AccessToken: token}, body)
	return resp
}

func (s *PlatformMailDeliveryTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformMailDeliveryTestSuite) mailValue(key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'mail_delivery' AND "key" = ?`,
		key,
	).Scan(&value))
	return value
}
