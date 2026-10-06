package settings

import (
	"strings"
	"testing"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/testutil"
)

const platformIndexSecret = "platform-index-http-secret"

// PlatformSettingsIndexTestSuite is GET /v1/platform/settings from S1.4.6.
// settings.view is the named permission; a platform_admins row is the gate
// because that name is not in a platform catalog. The same row stands in for
// a group ViewPermission that is not in that catalog.
type PlatformSettingsIndexTestSuite struct {
	authSuite
}

func TestPlatformSettingsIndexSuite(t *testing.T) {
	suite.Run(t, new(PlatformSettingsIndexTestSuite))
}

func (s *PlatformSettingsIndexTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	settings.FacadeCache{}.Forget("settings:platform:mail_smtp")
}

func (s *PlatformSettingsIndexTestSuite) TestAPlatformAdminSeesPlatformGroupsAndNotSecrets() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/mail_smtp",
		`{"host":"127.0.0.1","port":1,"encryption":"starttls","username":"mailer","password":"`+platformIndexSecret+`"}`)
	saved.AssertOk()

	listed := s.getRaw(session.AccessToken, "/v1/platform/settings")
	listed.AssertOk()
	raw, err := listed.Content()
	s.Require().NoError(err)
	if strings.Contains(raw, platformIndexSecret) || strings.Contains(raw, "enc:v1:") {
		s.Fail("the index included a secret")
	}
	s.NotContains(raw, "account_security")
	s.NotContains(raw, "account_webhooks")
	s.NotContains(raw, "account_sweep_limits")
	s.Contains(raw, `"deposit_scan"`)
	s.Contains(raw, `"webhook_delivery"`)
	s.Contains(raw, `"sweep_limits"`)
	s.Contains(raw, `"mail_smtp"`)
	s.Contains(raw, `"provider_etherscan"`)
	s.Contains(raw, `"settings.view"`)
	s.Contains(raw, `"scope":"platform"`)

	var body struct {
		Permissions struct {
			View   string `json:"view"`
			Update string `json:"update"`
		} `json:"permissions"`
		Sections []struct {
			Name   string `json:"name"`
			Blocks []struct {
				Groups []struct {
					Name      string `json:"name"`
					Scope     string `json:"scope"`
					ManagedBy string `json:"managed_by"`
					Fields    []struct {
						Key    string `json:"key"`
						Label  string `json:"label"`
						Type   string `json:"type"`
						Secret bool   `json:"secret"`
						IsSet  bool   `json:"is_set"`
						Value  any    `json:"value"`
					} `json:"fields"`
				} `json:"groups"`
			} `json:"blocks"`
		} `json:"sections"`
	}
	s.decode(listed, &body)
	s.Equal("settings.view", body.Permissions.View)
	s.Equal("settings.update", body.Permissions.Update)
	var passwordSeen bool
	for _, section := range body.Sections {
		for _, block := range section.Blocks {
			for _, group := range block.Groups {
				s.Equal("platform", group.Scope)
				if group.Name != "mail_smtp" {
					continue
				}
				for _, field := range group.Fields {
					if field.Key != "password" {
						continue
					}
					passwordSeen = true
					s.True(field.Secret)
					s.True(field.IsSet)
					s.Nil(field.Value)
					s.NotEmpty(field.Label)
					s.Equal("string", field.Type)
				}
			}
		}
	}
	s.True(passwordSeen)

	sealed := s.mailPassword()
	if !strings.HasPrefix(sealed, "enc:v1:") || strings.Contains(sealed, platformIndexSecret) {
		s.Fail("the password was not sealed")
	}
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND target_id = 'mail_smtp'`,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'platform.secret_viewed'`))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE metadata::text LIKE ?`,
		"%"+platformIndexSecret+"%",
	))
}

func (s *PlatformSettingsIndexTestSuite) TestANonAdminIsForbiddenAndAMissingSessionIsUnauthorized() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	forbidden := s.getRaw(session.AccessToken, "/v1/platform/settings")
	forbidden.AssertForbidden()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(forbidden, &body)
	s.Equal(responses.CodeForbidden, body.Error.Code)
	s.Equal("you do not have permission to view settings", body.Error.Message)
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL`))

	missing, err := s.Http(s.T()).Get("/v1/platform/settings")
	s.Require().NoError(err)
	missing.AssertUnauthorized()
}

func (s *PlatformSettingsIndexTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformSettingsIndexTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Put(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformSettingsIndexTestSuite) getRaw(token, path string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get(path)
	s.Require().NoError(err)
	return resp
}

func (s *PlatformSettingsIndexTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformSettingsIndexTestSuite) mailPassword() string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'mail_smtp' AND "key" = 'password'`,
	).Scan(&value))
	return value
}
