package accounts

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const accountSettingsHTTPSecret = "acct-s146-http-secret"

// PlatformAccountSettingsGroupTestSuite is
// GET /v1/platform/accounts/{accountId}/settings/{group} from S1.4.6:
// settings.view (platform-managed account groups). settings.view is not in a
// platform catalog, so a platform_admins row is the gate.
type PlatformAccountSettingsGroupTestSuite struct {
	authSuite
}

func TestPlatform_Account_SettingsGroupSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformAccountSettingsGroupTestSuite))
}

func (s *PlatformAccountSettingsGroupTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformAccountSettingsGroupTestSuite) TestAn_Admin_ReadsOneAccountAndNotAnotherOrASecret() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountA := s.createAccount("Account A")
	accountB := s.createAccount("Account B")

	empty := s.getRaw(session.AccessToken, s.groupPath(accountA, "account_sweep_limits"))
	empty.AssertOk()
	var defaults struct {
		Name      string `json:"name"`
		Scope     string `json:"scope"`
		ManagedBy string `json:"managed_by"`
		Fields    []struct {
			Key    string `json:"key"`
			Secret bool   `json:"secret"`
			IsSet  bool   `json:"is_set"`
			Value  any    `json:"value"`
		} `json:"fields"`
	}
	s.decode(empty, &defaults)
	s.Equal("account_sweep_limits", defaults.Name)
	s.Equal("account", defaults.Scope)
	s.Equal("platform", defaults.ManagedBy)
	var sawEVM, sawSolana bool
	for _, field := range defaults.Fields {
		s.False(field.Secret)
		s.False(field.IsSet)
		switch field.Key {
		case "max_addresses_evm":
			sawEVM = true
			s.Equal(float64(100), field.Value)
		case "max_addresses_solana":
			sawSolana = true
			s.Equal(float64(25), field.Value)
		}
	}
	s.True(sawEVM)
	s.True(sawSolana)

	s.insertSetting(&accountA, "account_sweep_limits", "max_addresses_evm", "17")
	s.insertSetting(&accountB, "account_sweep_limits", "max_addresses_evm", "19")
	s.insertSetting(&accountB, "account_sweep_limits", "daily_withdraw_cap_usd", "8.75")
	s.insertSetting(&accountB, "account_sweep_limits", "max_addresses_bitcoin", "19")
	s.insertSetting(&accountA, "account_webhooks", "signing_secret", "enc:v1:"+accountSettingsHTTPSecret)
	s.insertSetting(nil, "sweep_limits", "max_addresses_solana", "3")

	before := s.count(`SELECT count(*) FROM account_activity`)
	shown := s.getRaw(session.AccessToken, s.groupPath(accountA, "account_sweep_limits"))
	shown.AssertOk()
	raw, err := shown.Content()
	s.Require().NoError(err)
	if strings.Contains(raw, accountSettingsHTTPSecret) || strings.Contains(raw, "enc:v1:") || strings.Contains(raw, "8.75") {
		s.Fail("the group included a secret or the other account")
	}
	var body struct {
		Fields []struct {
			Key    string `json:"key"`
			Secret bool   `json:"secret"`
			IsSet  bool   `json:"is_set"`
			Value  any    `json:"value"`
		} `json:"fields"`
	}
	s.decode(shown, &body)
	var evm, bitcoin, solana bool
	for _, field := range body.Fields {
		s.False(field.Secret)
		switch field.Key {
		case "max_addresses_evm":
			evm = true
			s.True(field.IsSet)
			s.Equal(float64(17), field.Value)
		case "max_addresses_bitcoin":
			bitcoin = true
			s.False(field.IsSet)
			s.Equal(float64(100), field.Value)
		case "max_addresses_solana":
			solana = true
			s.False(field.IsSet)
			s.Equal(float64(25), field.Value)
		case "daily_withdraw_cap_usd":
			s.False(field.IsSet)
			s.Equal("", field.Value)
		}
	}
	s.True(evm)
	s.True(bitcoin)
	s.True(solana)
	s.Equal(before, s.count(`SELECT count(*) FROM account_activity`))
	s.Equal("19", s.settingValue(&accountB, "account_sweep_limits", "max_addresses_evm"))
	s.Equal("8.75", s.settingValue(&accountB, "account_sweep_limits", "daily_withdraw_cap_usd"))

	missingAccount := s.getRaw(session.AccessToken, s.groupPath(uuid.New(), "account_sweep_limits"))
	s.AssertError(missingAccount, 404, "not_found", "account not found")

	missingGroup := s.getRaw(session.AccessToken, s.groupPath(accountA, "no-such-group"))
	s.AssertError(missingGroup, 404, "not_found", "settings group not found")

	platformOnly := s.getRaw(session.AccessToken, s.groupPath(accountA, "mail_smtp"))
	s.AssertError(platformOnly, 404, "not_found", "settings group not found")

	accountManaged := s.getRaw(session.AccessToken, s.groupPath(accountA, "account_security"))
	s.AssertError(accountManaged, 404, "not_found", "settings group not found")

	badID := s.getRaw(session.AccessToken, "/v1/platform/accounts/not-a-uuid/settings/account_sweep_limits")
	s.AssertError(badID, 404, "not_found", "account not found")
	badIDUnknownGroup := s.getRaw(session.AccessToken, "/v1/platform/accounts/not-a-uuid/settings/no-such-group")
	s.AssertError(badIDUnknownGroup, 404, "not_found", "settings group not found")
}

func (s *PlatformAccountSettingsGroupTestSuite) TestA_Non_AdminIsForbiddenWhetherOrNotTheAccountAndGroupExist() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	accountID := s.createAccount("Known")

	// The group guard answers before the handler looks the account or the group
	// up, so a member cannot tell which ones exist.
	for _, path := range []string{
		s.groupPath(uuid.New(), "account_sweep_limits"),
		s.groupPath(accountID, "no-such-group"),
		s.groupPath(accountID, "sweep_limits"),
		"/v1/platform/accounts/not-a-uuid/settings/account_sweep_limits",
	} {
		refused := s.getRaw(session.AccessToken, path)
		s.AssertError(refused, 403, responses.CodeForbidden, "you do not have permission to view settings")
	}

	before := s.count(`SELECT count(*) FROM account_activity`)
	known := s.getRaw(session.AccessToken, s.groupPath(accountID, "account_sweep_limits"))
	s.AssertError(known, 403, responses.CodeForbidden, "you do not have permission to view settings")
	s.Equal(before, s.count(`SELECT count(*) FROM account_activity`))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits'`,
		accountID,
	))

	missing := s.Get(s.groupPath(accountID, "account_sweep_limits"), support.Session{})
	s.AssertError(missing, 401, "unauthorized", "missing or malformed bearer token")
}

func (s *PlatformAccountSettingsGroupTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountSettingsGroupTestSuite) createAccount(name string) uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: name, Status: "active", Environment: "prod",
	}))
	return accountID
}

func (s *PlatformAccountSettingsGroupTestSuite) insertSetting(accountID *uuid.UUID, group, key, value string) {
	s.T().Helper()
	if accountID == nil {
		_, err := facades.Orm().Query().Exec(
			`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
			 VALUES (NULL, ?, ?, ?, NOW(), NOW())`,
			group, key, value,
		)
		s.Require().NoError(err)
		return
	}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		*accountID, group, key, value,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountSettingsGroupTestSuite) settingValue(accountID *uuid.UUID, group, key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = ? AND "key" = ?`,
		accountID, group, key,
	).Scan(&value))
	return value
}

func (s *PlatformAccountSettingsGroupTestSuite) groupPath(accountID uuid.UUID, group string) string {
	return "/v1/platform/accounts/" + accountID.String() + "/settings/" + group
}

func (s *PlatformAccountSettingsGroupTestSuite) getRaw(token, path string) contractstesting.Response {
	s.T().Helper()
	resp := s.Get(path, support.Session{AccessToken: token})
	return resp
}

func (s *PlatformAccountSettingsGroupTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformAccountSettingsGroupTestSuite) errorMessage(resp contractstesting.Response) string {
	s.T().Helper()
	return s.errorBody(resp).Message
}

func (s *PlatformAccountSettingsGroupTestSuite) errorCode(resp contractstesting.Response) string {
	s.T().Helper()
	return s.errorBody(resp).Code
}

func (s *PlatformAccountSettingsGroupTestSuite) errorBody(resp contractstesting.Response) struct {
	Code    string `json:"code"`
	Message string `json:"message"`
} {
	s.T().Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	return body.Error
}
