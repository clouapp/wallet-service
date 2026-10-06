package settings

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	contractsmail "github.com/goravel/framework/contracts/mail"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/mails"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/testutil"
)

const (
	mailTestRecipient = "mail-test@example.test"
	mailTestHost      = "smtp.mail-test.invalid"
	mailTestSecret    = "mail-test-smtp-secret"
)

// PlatformMailTestSuite is POST /v1/platform/settings/mail/test from S1.4.6.
// settings.update and mail.update are not in a platform catalog, so a
// platform_admins row is the gate. The send goes through a fake mailer.
type PlatformMailTestSuite struct {
	authSuite
	sends *mailTestCapture
}

func TestPlatformMailTestSuite(t *testing.T) {
	suite.Run(t, new(PlatformMailTestSuite))
}

func (s *PlatformMailTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	settings.FacadeCache{}.Forget("settings:platform:mail_smtp")
	settings.FacadeCache{}.Forget("settings:platform:mail_delivery")
	_, err := facades.Orm().Query().Exec(`DELETE FROM settings WHERE account_id IS NULL AND "group" IN ('mail_smtp', 'mail_delivery')`)
	s.Require().NoError(err)
	appfacades.RestoreMailBaseline()
	s.sends = &mailTestCapture{}
	appfacades.SetMailSender(s.sends.send)
	s.T().Cleanup(func() {
		appfacades.SetMailSender(nil)
		appfacades.SetMailSendObserver(nil)
		appfacades.RestoreMailBaseline()
	})
}

func (s *PlatformMailTestSuite) TestAdminSendsOnceAndTheResponseHasNoPassword() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_smtp",
		`{"host":"`+mailTestHost+`","port":2525,"encryption":"tls","username":"mailer","password":"`+mailTestSecret+`"}`)
	saved.AssertOk()
	sealed := s.mailValue("password")
	if !strings.HasPrefix(sealed, "enc:v1:") || strings.Contains(sealed, mailTestSecret) {
		s.Fail("the password was not sealed")
	}
	before := s.count(`SELECT count(*) FROM account_activity`)

	raw := s.post(session.AccessToken, `{"to":"`+mailTestRecipient+`"}`, 200)
	s.refuseSecrets(raw)
	var body struct {
		Sent bool `json:"sent"`
	}
	s.Require().NoError(json.Unmarshal([]byte(raw), &body))
	s.True(body.Sent)
	s.Equal(1, s.sends.count())
	s.Equal([]string{mailTestRecipient}, s.sends.recipients())
	s.Equal(sealed, s.mailValue("password"))
	s.Equal(mailTestHost, s.mailValue("host"))
	s.Equal(before, s.count(`SELECT count(*) FROM account_activity`))
}

func (s *PlatformMailTestSuite) TestANonAdminIsForbiddenAndNothingIsSent() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	before := s.count(`SELECT count(*) FROM account_activity`)

	resp := s.postRaw(session.AccessToken, `{"to":"`+mailTestRecipient+`"}`)
	resp.AssertForbidden()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	s.Equal(responses.CodeForbidden, body.Error.Code)
	s.Equal("you do not have permission to update settings", body.Error.Message)
	s.Equal(0, s.sends.count())
	s.Equal(before, s.count(`SELECT count(*) FROM account_activity`))
}

func (s *PlatformMailTestSuite) TestAnInvalidAddressIsRejectedAndNothingIsSent() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	resp := s.postRaw(session.AccessToken, `{"to":"not-an-email"}`)
	resp.AssertUnprocessableEntity()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"validation_failed"`)
	s.Contains(content, `"to"`)
	s.Equal(0, s.sends.count())
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp'`))
}

func (s *PlatformMailTestSuite) TestAMailerFailureIsBadGatewayWithoutCredentials() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	s.sends.fail = errors.New("dial " + mailTestHost + " password " + mailTestSecret)

	raw := s.post(session.AccessToken, `{"to":"`+mailTestRecipient+`"}`, 502)
	s.refuseSecrets(raw)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(raw), &body))
	s.Equal(responses.CodeProviderUnavailable, body.Error.Code)
	s.Equal("the test message was not sent", body.Error.Message)
	s.Equal(1, s.sends.count())
}

func (s *PlatformMailTestSuite) TestGetMailGroupIsNotTheTestRoute() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	resp := s.getRaw(session.AccessToken, "/v1/platform/settings/mail")
	resp.AssertNotFound()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	s.Equal(responses.CodeNotFound, body.Error.Code)
	s.Equal("settings group not found", body.Error.Message)
	s.Equal(0, s.sends.count())
}

func (s *PlatformMailTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformMailTestSuite) post(token, body string, status int) string {
	s.T().Helper()
	resp := s.postRaw(token, body)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}

func (s *PlatformMailTestSuite) postRaw(token, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Post("/v1/platform/settings/mail/test", strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformMailTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Put(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformMailTestSuite) getRaw(token, path string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get(path)
	s.Require().NoError(err)
	return resp
}

func (s *PlatformMailTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformMailTestSuite) mailValue(key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp' AND "key" = ?`,
		key,
	).Scan(&value))
	return value
}

func (s *PlatformMailTestSuite) refuseSecrets(body string) {
	s.T().Helper()
	if strings.Contains(body, mailTestSecret) || strings.Contains(body, mailTestHost) || strings.Contains(body, "enc:v1:") {
		s.Fail("the response included mail credentials")
	}
}

type mailTestCapture struct {
	mu   sync.Mutex
	n    int
	to   []string
	fail error
}

func (c *mailTestCapture) send(mailable ...contractsmail.Mailable) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	if len(mailable) == 1 {
		if mail, ok := mailable[0].(*mails.SettingsTestMail); ok && mail != nil && mail.Envelope() != nil {
			c.to = append([]string{}, mail.Envelope().To...)
		}
	}
	return c.fail
}

func (c *mailTestCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *mailTestCapture) recipients() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.to...)
}
