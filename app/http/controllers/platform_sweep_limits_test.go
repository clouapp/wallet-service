package controllers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/tests/testutil"
)

// PlatformSweepLimitsTestSuite is PUT /v1/platform/settings/sweep_limits from
// S1.4.4. A platform_admins row is the gate. The activity row names the group
// and the fields, never the values.
type PlatformSweepLimitsTestSuite struct {
	authSuite
}

func TestPlatformSweepLimitsSuite(t *testing.T) {
	suite.Run(t, new(PlatformSweepLimitsTestSuite))
}

func (s *PlatformSweepLimitsTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	settings.FacadeCache{}.Forget("settings:platform:sweep_limits")
}

func (s *PlatformSweepLimitsTestSuite) TestAPlatformRowChangesLoadLimitsAndTheAccountRowWins() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountID := s.seedOwnedAccount(admin.ID)
	s.forgetSweepCache(accountID)

	missing := s.loadLimits(accountID)
	s.Equal(100, missing.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(25, missing.MaxAddressesPerRequest[models.AdapterTypeSolana])
	s.Equal(100, missing.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	s.Equal(50, missing.MaxConsolidateReqPerDay)
	s.Nil(missing.DailyWithdrawCapUSD)
	s.Equal(int64(0), s.platformSweepCount())

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/sweep_limits",
		`{"max_addresses_evm":40,"max_addresses_solana":12,"max_addresses_bitcoin":30,"max_consolidate_requests_per_day":8,"daily_withdraw_cap_usd":""}`)
	saved.AssertOk()
	s.Equal("", s.platformValue("daily_withdraw_cap_usd"))
	s.Equal("40", s.platformValue("max_addresses_evm"))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = 'sweep_limits'
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = '{"fields":["daily_withdraw_cap_usd","max_addresses_bitcoin","max_addresses_evm","max_addresses_solana","max_consolidate_requests_per_day"],"group":"sweep_limits"}'::jsonb`,
		admin.ID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND metadata::text LIKE '%40%'`,
	))

	fromPlatform := s.loadLimits(accountID)
	s.Equal(40, fromPlatform.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(12, fromPlatform.MaxAddressesPerRequest[models.AdapterTypeSolana])
	s.Equal(30, fromPlatform.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	s.Equal(8, fromPlatform.MaxConsolidateReqPerDay)
	s.Nil(fromPlatform.DailyWithdrawCapUSD)

	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, 'account_sweep_limits', 'max_addresses_evm', '7', NOW(), NOW())`,
		accountID,
	)
	s.Require().NoError(err)
	s.forgetSweepCache(accountID)
	overridden := s.loadLimits(accountID)
	s.Equal(7, overridden.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(12, overridden.MaxAddressesPerRequest[models.AdapterTypeSolana])
	s.Nil(overridden.DailyWithdrawCapUSD)

	_, err = facades.Orm().Query().Exec(
		`UPDATE settings SET value = '0'
		 WHERE account_id IS NULL AND "group" = 'sweep_limits' AND "key" = 'max_addresses_solana'`,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`UPDATE settings SET value = '-1'
		 WHERE account_id IS NULL AND "group" = 'sweep_limits' AND "key" = 'daily_withdraw_cap_usd'`,
	)
	s.Require().NoError(err)
	s.forgetSweepCache(accountID)
	invalid := s.loadLimits(accountID)
	s.Equal(7, invalid.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(25, invalid.MaxAddressesPerRequest[models.AdapterTypeSolana])
	s.Equal(30, invalid.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	s.Nil(invalid.DailyWithdrawCapUSD)

	_, err = facades.Orm().Query().Exec(
		`DELETE FROM settings WHERE account_id IS NULL AND "group" = 'sweep_limits'`,
	)
	s.Require().NoError(err)
	s.forgetSweepCache(accountID)
	withoutPlatform := s.loadLimits(accountID)
	s.Equal(7, withoutPlatform.MaxAddressesPerRequest[models.AdapterTypeEVM])
	s.Equal(25, withoutPlatform.MaxAddressesPerRequest[models.AdapterTypeSolana])
	s.Equal(100, withoutPlatform.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	s.Equal(50, withoutPlatform.MaxConsolidateReqPerDay)
	s.Nil(withoutPlatform.DailyWithdrawCapUSD)
}

func (s *PlatformSweepLimitsTestSuite) TestZeroNegativeAndANegativeCapAreNotStored() {
	member := s.seedUser(false)
	memberSession := s.signIn(member.Email)
	forbidden := s.putRaw(memberSession.AccessToken, "/v1/platform/settings/sweep_limits",
		`{"max_addresses_evm":40,"max_addresses_solana":12,"max_addresses_bitcoin":30,"max_consolidate_requests_per_day":8}`)
	forbidden.AssertForbidden()
	s.Equal(int64(0), s.platformSweepCount())

	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	for _, body := range []string{
		`{"max_addresses_evm":0,"max_addresses_solana":12,"max_addresses_bitcoin":30,"max_consolidate_requests_per_day":8}`,
		`{"max_addresses_evm":-2,"max_addresses_solana":12,"max_addresses_bitcoin":30,"max_consolidate_requests_per_day":8}`,
		`{"max_addresses_evm":40,"max_addresses_solana":12,"max_addresses_bitcoin":30,"max_consolidate_requests_per_day":8,"daily_withdraw_cap_usd":"-1.50"}`,
	} {
		rejected := s.putRaw(session.AccessToken, "/v1/platform/settings/sweep_limits", body)
		rejected.AssertUnprocessableEntity()
		s.assertValidationFailed(rejected)
	}
	s.Equal(int64(0), s.platformSweepCount())
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))

	saved := s.putRaw(session.AccessToken, "/v1/platform/settings/sweep_limits",
		`{"max_addresses_evm":40,"max_addresses_solana":12,"max_addresses_bitcoin":30,"max_consolidate_requests_per_day":8,"daily_withdraw_cap_usd":""}`)
	saved.AssertOk()
	rejected := s.putRaw(session.AccessToken, "/v1/platform/settings/sweep_limits",
		`{"max_addresses_evm":0,"daily_withdraw_cap_usd":"-1"}`)
	rejected.AssertUnprocessableEntity()
	s.assertValidationFailed(rejected)
	s.Equal("40", s.platformValue("max_addresses_evm"))
	s.Equal("", s.platformValue("daily_withdraw_cap_usd"))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'sweep_limits' AND value LIKE '-%'`,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'sweep_limits' AND "key" = 'daily_withdraw_cap_usd' AND value = '0'`,
	))
}

func (s *PlatformSweepLimitsTestSuite) loadLimits(accountID uuid.UUID) *sweep.Limits {
	s.T().Helper()
	limits, err := container.Get().SweepService.LoadLimits(context.Background(), accountID)
	s.Require().NoError(err)
	s.Require().NotNil(limits)
	return limits
}

func (s *PlatformSweepLimitsTestSuite) forgetSweepCache(accountID uuid.UUID) {
	s.T().Helper()
	settings.FacadeCache{}.Forget("settings:platform:sweep_limits")
	settings.FacadeCache{}.Forget("settings:account:" + accountID.String() + ":account_sweep_limits")
}

func (s *PlatformSweepLimitsTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformSweepLimitsTestSuite) seedOwnedAccount(userID uuid.UUID) uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "sweep-" + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: models.AccountRoleOwner, Status: models.MembershipStatusActive,
	}))
	return accountID
}

func (s *PlatformSweepLimitsTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Put(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformSweepLimitsTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformSweepLimitsTestSuite) platformSweepCount() int64 {
	s.T().Helper()
	return s.count(`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'sweep_limits'`)
}

func (s *PlatformSweepLimitsTestSuite) platformValue(key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'sweep_limits' AND "key" = ?`,
		key,
	).Scan(&value))
	return value
}

func (s *PlatformSweepLimitsTestSuite) assertValidationFailed(resp contractstesting.Response) {
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
