package accounts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// PlatformAccountSweepLimitsTestSuite is
// PUT /v1/platform/accounts/{accountId}/settings/{group}
// settings.update + sweep.update for account_sweep_limits.
// Those names are not in a platform catalog, so a platform_admins row is the gate.
type PlatformAccountSweepLimitsTestSuite struct {
	authSuite
}

func TestPlatformAccountSweepLimitsSuite(t *testing.T) {
	suite.Run(t, new(PlatformAccountSweepLimitsTestSuite))
}

func (s *PlatformAccountSweepLimitsTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformAccountSweepLimitsTestSuite) TestAnAdminWriteOverridesThePlatformRowForLoadLimits() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountID := s.seedOwnedAccount(admin.ID)
	otherID := s.createAccount("Other")
	s.insertPlatformSweep("max_addresses_evm", "40")
	s.insertPlatformSweep("max_addresses_solana", "12")
	s.insertPlatformSweep("daily_withdraw_cap_usd", "9.50")
	s.insertAccountSweep(otherID, "max_addresses_evm", "19")
	s.forgetSweepCache(accountID)
	s.forgetSweepCache(otherID)

	before := s.loadLimits(accountID)
	s.Equal(40, before.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(12, before.MaxAddressesPerRequest[models.AdapterTypeSolana])
	s.Require().NotNil(before.DailyWithdrawCapUSD)
	s.True(before.DailyWithdrawCapUSD.Equal(decimal.RequireFromString("9.50")))

	saved := s.putRaw(session.AccessToken, s.groupPath(accountID, "account_sweep_limits"),
		`{"max_addresses_evm":7,"daily_withdraw_cap_usd":""}`)
	saved.AssertOk()
	s.Equal("7", s.accountValue(accountID, "max_addresses_evm"))
	s.Equal("", s.accountValue(accountID, "daily_withdraw_cap_usd"))
	s.Equal("40", s.platformValue("max_addresses_evm"))
	s.Equal("19", s.accountValue(otherID, "max_addresses_evm"))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND value LIKE '-%'`,
		accountID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND "key" = 'daily_withdraw_cap_usd' AND value = '0'`,
		accountID,
	))

	after := s.loadLimits(accountID)
	s.Equal(7, after.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(12, after.MaxAddressesPerRequest[models.AdapterTypeSolana])
	s.Equal(100, after.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	s.Equal(50, after.MaxConsolidateReqPerDay)
	s.Nil(after.DailyWithdrawCapUSD)
	other := s.loadLimits(otherID)
	s.Equal(19, other.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(12, other.MaxAddressesPerRequest[models.AdapterTypeSolana])

	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = 'account_sweep_limits'
		   AND account_id = ? AND actor_user_id = ?
		   AND metadata = '{"fields":["daily_withdraw_cap_usd","max_addresses_evm"],"group":"account_sweep_limits"}'::jsonb`,
		accountID, admin.ID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND account_id IS NULL AND target_id = 'account_sweep_limits'`,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND metadata::text LIKE '%7%'`,
	))

	page := s.listAccountActivity(session.AccessToken, accountID)
	var saw bool
	for _, row := range page.Data {
		if row.Action != "settings.updated" || row.TargetID != "account_sweep_limits" {
			continue
		}
		saw = true
		s.Equal(accountID.String(), row.AccountID)
		s.Equal("account_sweep_limits", row.Metadata.Group)
		s.ElementsMatch([]string{"daily_withdraw_cap_usd", "max_addresses_evm"}, row.Metadata.Fields)
	}
	s.True(saw)
	encoded, err := json.Marshal(page)
	s.Require().NoError(err)
	s.NotContains(string(encoded), `"7"`)
	s.NotContains(string(encoded), "9.50")

	platformPage := s.listPlatformActivity(session.AccessToken)
	for _, row := range platformPage.Data {
		s.NotEqual("account_sweep_limits", row.TargetID)
	}

	shown := s.getRaw(session.AccessToken, s.groupPath(accountID, "account_sweep_limits"))
	shown.AssertOk()
	raw, err := shown.Content()
	s.Require().NoError(err)
	s.NotContains(raw, "9.50")
	s.NotContains(raw, "enc:v1:")
}

func (s *PlatformAccountSweepLimitsTestSuite) TestAnotherGroupAndAnUnknownAccountAreNotFoundBeforeForbidden() {
	member := s.seedUser(false)
	memberSession := s.signIn(member.Email)
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	adminSession := s.signIn(admin.Email)
	accountID := s.createAccount("Known")
	body := `{"max_addresses_evm":7,"daily_withdraw_cap_usd":""}`

	for _, group := range []string{"no-such-group", "sweep_limits", "mail_smtp", "account_security", "account_webhooks"} {
		for _, token := range []string{memberSession.AccessToken, adminSession.AccessToken} {
			missing := s.putRaw(token, s.groupPath(accountID, group), body)
			missing.AssertNotFound()
			s.Equal("settings group not found", s.errorMessage(missing))
			s.Equal(responses.CodeNotFound, s.errorCode(missing))
		}
	}
	badID := s.putRaw(adminSession.AccessToken, "/v1/platform/accounts/not-a-uuid/settings/account_sweep_limits", body)
	badID.AssertNotFound()
	s.Equal("account not found", s.errorMessage(badID))
	badIDOtherGroup := s.putRaw(memberSession.AccessToken, "/v1/platform/accounts/not-a-uuid/settings/no-such-group", body)
	badIDOtherGroup.AssertNotFound()
	s.Equal("settings group not found", s.errorMessage(badIDOtherGroup))

	unknownAccount := s.putRaw(memberSession.AccessToken, s.groupPath(uuid.New(), "account_sweep_limits"), body)
	unknownAccount.AssertNotFound()
	s.Equal("account not found", s.errorMessage(unknownAccount))
	s.Equal(responses.CodeNotFound, s.errorCode(unknownAccount))
	unknownForAdmin := s.putRaw(adminSession.AccessToken, s.groupPath(uuid.New(), "account_sweep_limits"), body)
	unknownForAdmin.AssertNotFound()
	s.Equal("account not found", s.errorMessage(unknownForAdmin))

	known := s.putRaw(memberSession.AccessToken, s.groupPath(accountID, "account_sweep_limits"), body)
	known.AssertForbidden()
	s.Equal(responses.CodeForbidden, s.errorCode(known))
	s.Equal("you do not have permission to update settings", s.errorMessage(known))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits'`,
		accountID,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))

	missing, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Put(s.groupPath(accountID, "account_sweep_limits"), strings.NewReader(body))
	s.Require().NoError(err)
	missing.AssertUnauthorized()
}

func (s *PlatformAccountSweepLimitsTestSuite) TestZeroNegativeAndANegativeCapLeaveTheRowUnchanged() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountID := s.createAccount("Limits")
	s.insertAccountSweep(accountID, "max_addresses_evm", "17")
	s.insertAccountSweep(accountID, "daily_withdraw_cap_usd", "1.25")
	before := s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`)

	for _, body := range []string{
		`{"max_addresses_evm":0,"max_addresses_solana":12,"max_addresses_bitcoin":30,"max_consolidate_requests_per_day":8}`,
		`{"max_addresses_evm":-2,"max_addresses_solana":12,"max_addresses_bitcoin":30,"max_consolidate_requests_per_day":8}`,
		`{"max_addresses_evm":40,"max_addresses_solana":12,"max_addresses_bitcoin":30,"max_consolidate_requests_per_day":8,"daily_withdraw_cap_usd":"-1.50"}`,
	} {
		rejected := s.putRaw(session.AccessToken, s.groupPath(accountID, "account_sweep_limits"), body)
		rejected.AssertUnprocessableEntity()
		s.assertValidationFailed(rejected)
	}
	s.Equal("17", s.accountValue(accountID, "max_addresses_evm"))
	s.Equal("1.25", s.accountValue(accountID, "daily_withdraw_cap_usd"))
	s.Equal(int64(2), s.count(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits'`,
		accountID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND value LIKE '-%'`,
		accountID,
	))
	s.Equal(before, s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
	limits := s.loadLimits(accountID)
	s.Equal(17, limits.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Require().NotNil(limits.DailyWithdrawCapUSD)
	s.True(limits.DailyWithdrawCapUSD.Equal(decimal.RequireFromString("1.25")))
}

func (s *PlatformAccountSweepLimitsTestSuite) loadLimits(accountID uuid.UUID) *sweep.Limits {
	s.T().Helper()
	limits, err := container.Get().SweepService.LoadLimits(context.Background(), accountID)
	s.Require().NoError(err)
	s.Require().NotNil(limits)
	return limits
}

func (s *PlatformAccountSweepLimitsTestSuite) forgetSweepCache(accountID uuid.UUID) {
	s.T().Helper()
	settings.FacadeCache{}.Forget("settings:platform:sweep_limits")
	settings.FacadeCache{}.Forget("settings:account:" + accountID.String() + ":account_sweep_limits")
}

func (s *PlatformAccountSweepLimitsTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountSweepLimitsTestSuite) createAccount(name string) uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: name, Status: "active", Environment: "prod",
	}))
	return accountID
}

func (s *PlatformAccountSweepLimitsTestSuite) seedOwnedAccount(userID uuid.UUID) uuid.UUID {
	s.T().Helper()
	accountID := s.createAccount("sweep-" + accountIDString(userID))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: models.AccountRoleOwner, Status: models.MembershipStatusActive,
	}))
	return accountID
}

func accountIDString(id uuid.UUID) string {
	text := id.String()
	if len(text) > 8 {
		return text[:8]
	}
	return text
}

func (s *PlatformAccountSweepLimitsTestSuite) insertPlatformSweep(key, value string) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, 'sweep_limits', ?, ?, NOW(), NOW())`,
		key, value,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountSweepLimitsTestSuite) insertAccountSweep(accountID uuid.UUID, key, value string) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', ?, ?, NOW(), NOW())`,
		accountID, key, value,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountSweepLimitsTestSuite) accountValue(accountID uuid.UUID, key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND "key" = ?`,
		accountID, key,
	).Scan(&value))
	return value
}

func (s *PlatformAccountSweepLimitsTestSuite) platformValue(key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'sweep_limits' AND "key" = ?`,
		key,
	).Scan(&value))
	return value
}

func (s *PlatformAccountSweepLimitsTestSuite) groupPath(accountID uuid.UUID, group string) string {
	return "/v1/platform/accounts/" + accountID.String() + "/settings/" + group
}

func (s *PlatformAccountSweepLimitsTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Put(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformAccountSweepLimitsTestSuite) getRaw(token, path string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get(path)
	s.Require().NoError(err)
	return resp
}

func (s *PlatformAccountSweepLimitsTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformAccountSweepLimitsTestSuite) errorMessage(resp contractstesting.Response) string {
	s.T().Helper()
	return s.errorBody(resp).Message
}

func (s *PlatformAccountSweepLimitsTestSuite) errorCode(resp contractstesting.Response) string {
	s.T().Helper()
	return s.errorBody(resp).Code
}

func (s *PlatformAccountSweepLimitsTestSuite) errorBody(resp contractstesting.Response) struct {
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

func (s *PlatformAccountSweepLimitsTestSuite) assertValidationFailed(resp contractstesting.Response) {
	s.T().Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Errors map[string][]string `json:"errors"`
	}
	s.decode(resp, &body)
	s.Equal(responses.CodeValidationFailed, body.Error.Code)
	s.Equal("validation failed", body.Error.Message)
	s.NotEmpty(body.Errors)
}

type accountSweepActivityPage struct {
	Data []struct {
		AccountID  string `json:"account_id"`
		Action     string `json:"action"`
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Metadata   struct {
			Group  string   `json:"group"`
			Fields []string `json:"fields"`
		} `json:"metadata"`
	} `json:"data"`
}

func (s *PlatformAccountSweepLimitsTestSuite) listAccountActivity(token string, accountID uuid.UUID) accountSweepActivityPage {
	s.T().Helper()
	resp := s.getRaw(token, "/v1/accounts/"+accountID.String()+"/activity")
	resp.AssertOk()
	var page accountSweepActivityPage
	s.decode(resp, &page)
	return page
}

func (s *PlatformAccountSweepLimitsTestSuite) listPlatformActivity(token string) accountSweepActivityPage {
	s.T().Helper()
	resp := s.getRaw(token, "/v1/platform/activity")
	resp.AssertOk()
	var page accountSweepActivityPage
	s.decode(resp, &page)
	return page
}
