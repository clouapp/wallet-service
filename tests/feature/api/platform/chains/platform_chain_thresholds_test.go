package chains

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// PlatformChainThresholdTestSuite is PATCH /v1/platform/chains/{chainId} from
// S1.4.4. S1.4.7 declares chains.view and chains.update on the catalog entry
// this route already uses. A platform_admins row remains the gate.
type PlatformChainThresholdTestSuite struct {
	authSuite
}

func TestPlatform_Chain_ThresholdSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformChainThresholdTestSuite))
}

func (s *PlatformChainThresholdTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformChainThresholdTestSuite) TestA_Platform_AdminUpdatesOneThresholdTheSweepReaderSees() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountID := s.seedOwnedAccount(admin.ID)

	before := s.loadChain(models.ChainETH)
	beforeGas := before.GasReadinessThreshold().String()
	beforeDust := before.DustThresholdNative().String()
	beforeUSD := before.DustThresholdUSD.Decimal
	beforeConfirmations := before.RequiredConfirmations
	s.NotEqual("77", beforeGas)

	body := s.patchChain(session.AccessToken, models.ChainETH, `{"gas_readiness_threshold_raw":"77"}`, http.StatusOK)
	s.Equal(models.ChainETH, body["id"])
	s.Equal("77", body["gas_readiness_threshold_raw"])
	s.Equal(beforeDust, body["dust_threshold_native_raw"])
	s.NotContains(body, "rpc_url")

	seen := s.loadChain(models.ChainETH)
	s.Equal("77", seen.GasReadinessThreshold().String())
	s.Equal(beforeDust, seen.DustThresholdNative().String())
	s.True(beforeUSD.Equal(seen.DustThresholdUSD.Decimal))
	s.Equal(beforeConfirmations, seen.RequiredConfirmations)

	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'chains.updated' AND target_type = 'chain' AND target_id = ?
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = '{"fields":["gas_readiness_threshold_raw"],"key":"eth"}'::jsonb`,
		models.ChainETH, admin.ID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'chains.updated' AND account_id IS NOT NULL`,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'platform.secret_viewed'`,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'chains.updated' AND metadata::text LIKE '%77%'`,
	))

	account := s.accountActivity(session.AccessToken, accountID)
	account.AssertOk()
	var accountRows struct {
		Data []struct {
			Action string `json:"action"`
		} `json:"data"`
	}
	s.decode(account, &accountRows)
	for _, row := range accountRows.Data {
		s.NotEqual("chains.updated", row.Action)
	}

	platform := s.platformActivity(session.AccessToken)
	platform.AssertOk()
	var listed struct {
		Data []struct {
			Action    string  `json:"action"`
			AccountID *string `json:"account_id"`
			TargetID  string  `json:"target_id"`
			Metadata  struct {
				Key    string   `json:"key"`
				Fields []string `json:"fields"`
			} `json:"metadata"`
		} `json:"data"`
	}
	s.decode(platform, &listed)
	seenActivity := 0
	for _, row := range listed.Data {
		if row.Action != "chains.updated" {
			continue
		}
		seenActivity++
		s.Nil(row.AccountID)
		s.Equal(models.ChainETH, row.TargetID)
		s.Equal("eth", row.Metadata.Key)
		s.Equal([]string{"gas_readiness_threshold_raw"}, row.Metadata.Fields)
	}
	s.Equal(1, seenActivity)
}

func (s *PlatformChainThresholdTestSuite) TestA_Non_AdminIsForbiddenAndAnUnknownChainIsNotFoundFirst() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	before := s.loadChain(models.ChainETH)

	forbidden := s.patchRaw(session.AccessToken, "/v1/platform/chains/eth", `{"dust_threshold_usd":"-1"}`)
	forbidden.AssertForbidden()
	s.AssertError(forbidden, 403, responses.CodeForbidden, "you do not have permission to update chains")
	after := s.loadChain(models.ChainETH)
	s.True(before.DustThresholdUSD.Decimal.Equal(after.DustThresholdUSD.Decimal))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'chains.updated'`))

	missing := s.patchRaw(session.AccessToken, "/v1/platform/chains/no-such-chain", `{"dust_threshold_usd":"-1"}`)
	missing.AssertNotFound()
	s.AssertError(missing, 404, responses.CodeNotFound, "chain not found")

	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	adminSession := s.signIn(admin.Email)
	adminMissing := s.patchRaw(adminSession.AccessToken, "/v1/platform/chains/no-such-chain", `{"gas_readiness_threshold_raw":"1"}`)
	adminMissing.AssertNotFound()
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'chains.updated'`))
}

func (s *PlatformChainThresholdTestSuite) TestA_Negative_ValueIsValidationFailedAndTheRowStays() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	before := s.loadChain(models.ChainETH)

	negativeAmount := s.patchRaw(session.AccessToken, "/v1/platform/chains/eth", `{"dust_threshold_usd":"-1","gas_readiness_threshold_raw":"9"}`)
	negativeAmount.AssertUnprocessableEntity()
	s.assertValidation(negativeAmount, "dust_threshold_usd", "must not be negative")
	unchanged := s.loadChain(models.ChainETH)
	s.Equal(before.GasReadinessThreshold().String(), unchanged.GasReadinessThreshold().String())
	s.True(before.DustThresholdUSD.Decimal.Equal(unchanged.DustThresholdUSD.Decimal))

	negativeCount := s.patchRaw(session.AccessToken, "/v1/platform/chains/eth", `{"required_confirmations":-1}`)
	negativeCount.AssertUnprocessableEntity()
	s.assertValidation(negativeCount, "required_confirmations", "must not be negative")
	still := s.loadChain(models.ChainETH)
	s.Equal(before.RequiredConfirmations, still.RequiredConfirmations)
	s.Equal(before.GasReadinessThreshold().String(), still.GasReadinessThreshold().String())
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'chains.updated'`))

	emptyGas := s.patchRaw(session.AccessToken, "/v1/platform/chains/eth", `{"gas_readiness_threshold_raw":""}`)
	emptyGas.AssertUnprocessableEntity()
	s.assertValidation(emptyGas, "gas_readiness_threshold_raw", "must not be empty")

	btcBefore := s.loadChain(models.ChainBTC)
	cleared := s.patchChain(session.AccessToken, models.ChainBTC, `{"gas_readiness_threshold_raw":""}`, http.StatusOK)
	s.Equal("", cleared["gas_readiness_threshold_raw"])
	btc := s.loadChain(models.ChainBTC)
	s.Nil(btc.GasReadinessThreshold())
	s.Equal(deref(btcBefore.DustThresholdNativeRaw), deref(btc.DustThresholdNativeRaw))
	s.True(btcBefore.DustThresholdUSD.Decimal.Equal(btc.DustThresholdUSD.Decimal))
}

func (s *PlatformChainThresholdTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformChainThresholdTestSuite) seedOwnedAccount(userID uuid.UUID) uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "chains-" + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: models.AccountRoleOwner, Status: models.MembershipStatusActive,
	}))
	return accountID
}

func (s *PlatformChainThresholdTestSuite) loadChain(id string) *models.Chain {
	s.T().Helper()
	var chain models.Chain
	s.Require().NoError(facades.Orm().Query().Where("id", id).First(&chain))
	s.Require().Equal(id, chain.ID)
	return &chain
}

func (s *PlatformChainThresholdTestSuite) patchChain(token, chainID, body string, status int) map[string]any {
	s.T().Helper()
	resp := s.patchRaw(token, "/v1/platform/chains/"+chainID, body)
	resp.AssertStatus(status)
	var parsed map[string]any
	s.decode(resp, &parsed)
	if status == http.StatusOK {
		usd, ok := parsed["dust_threshold_usd"].(string)
		s.Require().True(ok)
		_, err := decimal.NewFromString(usd)
		s.Require().NoError(err)
	}
	return parsed
}

func (s *PlatformChainThresholdTestSuite) patchRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp := s.Patch(path, support.Session{AccessToken: token}, body)
	return resp
}

func (s *PlatformChainThresholdTestSuite) accountActivity(bearer string, accountID uuid.UUID) contractstesting.Response {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String()+"/activity", support.Session{AccessToken: bearer})
	return resp
}

func (s *PlatformChainThresholdTestSuite) platformActivity(bearer string) contractstesting.Response {
	s.T().Helper()
	resp := s.Get("/v1/platform/activity", support.Session{AccessToken: bearer})
	return resp
}

func (s *PlatformChainThresholdTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformChainThresholdTestSuite) assertValidation(resp contractstesting.Response, field, message string) {
	s.T().Helper()
	s.AssertError(resp, 422, responses.CodeValidationFailed, "validation failed")
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Errors map[string][]string `json:"errors"`
	}
	s.decode(resp, &body)
	s.Equal([]string{message}, body.Errors[field])
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
