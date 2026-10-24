package settings

import (
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const ingestPushPath = "/v1/platform/settings/ingest_push"

// PlatformIngestPushTestSuite is PUT /v1/platform/settings/ingest_push: the base
// URL Alchemy, Helius and QuickNode post deposits to. A platform_admins row is
// the gate. The value is not a secret and is returned in the clear; one a provider
// cannot post to (egress.ParsePublicBase) is 422 with the rule's sentence and is
// not stored.
type PlatformIngestPushTestSuite struct {
	authSuite
}

func TestPlatform_Ingest_PushSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformIngestPushTestSuite))
}

func (s *PlatformIngestPushTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	settings.FacadeCache{}.Forget("settings:platform:ingest_push")
}

func (s *PlatformIngestPushTestSuite) TestA_Platform_AdminStoresTheBaseURLInTheClear() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	saved := s.Put(ingestPushPath, support.Session{AccessToken: session.AccessToken}, `{"base_url":" https://push.wallets.example.com/waas "}`)
	saved.AssertOk()
	s.Equal("https://push.wallets.example.com/waas", s.baseURLField(saved))
	s.Equal("https://push.wallets.example.com/waas", s.storedBaseURL())
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = 'ingest_push'
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = '{"fields":["base_url"],"group":"ingest_push"}'::jsonb`,
		admin.ID,
	))

	shown := s.Get(ingestPushPath, support.Session{AccessToken: session.AccessToken})
	shown.AssertOk()
	s.Equal("https://push.wallets.example.com/waas", s.baseURLField(shown))
}

func (s *PlatformIngestPushTestSuite) TestA_BaseURLAProviderCannotPostToIsRefusedAndNotStored() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)

	for base, reason := range map[string]string{
		"http://push.wallets.example.com":            egress.ReasonScheme,
		"https://user:pass@push.wallets.example.com": egress.ReasonUserinfo,
		"https://push.wallets.example.com/?a=1":      egress.ReasonBaseURL,
		"https://169.254.169.254":                    egress.ReasonLinkLocal,
		"https://169.254.169.254.":                   egress.ReasonLinkLocal,
		"https://2130706433":                         egress.ReasonHostName,
		"https://localhost.":                         egress.ReasonLoopback,
		"https://metadata.google.internal":           egress.ReasonMetadata,
		"https://[64:ff9b::a9fe:a9fe]":               egress.ReasonLinkLocal,
		"https://push.wallets.example.com:25":        egress.ReasonPort,
	} {
		rejected := s.Put(ingestPushPath, support.Session{AccessToken: session.AccessToken}, `{"base_url":"`+base+`"}`)
		s.AssertError(rejected, 422, responses.CodeValidationFailed, "validation failed")
		var body struct {
			Errors map[string][]string `json:"errors"`
		}
		s.decode(rejected, &body)
		s.Equal([]string{reason}, body.Errors["base_url"], base)
		raw, err := rejected.Content()
		s.Require().NoError(err)
		s.NotContains(raw, egress.Host(base), "the answer names the rule, never the URL")
	}
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'ingest_push'`))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE target_id = 'ingest_push'`))
}

func (s *PlatformIngestPushTestSuite) TestA_Non_AdminIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)

	forbidden := s.Put(ingestPushPath, support.Session{AccessToken: session.AccessToken}, `{"base_url":"https://push.wallets.example.com"}`)

	s.AssertError(forbidden, 403, responses.CodeForbidden, "you do not have permission to update settings")
	s.Equal(int64(0), s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'ingest_push'`))
}

func (s *PlatformIngestPushTestSuite) baseURLField(resp contractstesting.Response) any {
	s.T().Helper()
	var view struct {
		Name   string `json:"name"`
		Fields []struct {
			Key    string `json:"key"`
			Secret bool   `json:"secret"`
			Value  any    `json:"value"`
		} `json:"fields"`
	}
	s.decode(resp, &view)
	s.Equal("ingest_push", view.Name)
	for _, field := range view.Fields {
		if field.Key == "base_url" {
			s.False(field.Secret)
			return field.Value
		}
	}
	s.Fail("the view has no base_url field")
	return nil
}

func (s *PlatformIngestPushTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformIngestPushTestSuite) storedBaseURL() string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'ingest_push' AND "key" = 'base_url'`,
	).Scan(&value))
	return value
}

func (s *PlatformIngestPushTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}
