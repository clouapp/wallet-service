package chains

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	evmchain "github.com/macrowallets/waas/app/adapters/chain/evm"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/security"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// PlatformChainRPCTestSuite is PATCH /v1/platform/chains/{chainId}/rpc from
// S1.4.4. S1.4.7 declares chains.view and chains.update on the catalog entry
// this route already uses. A platform_admins row remains the gate. The URL
// is write-only and is never returned.
type PlatformChainRPCTestSuite struct {
	authSuite
}

func TestPlatform_Chain_RPCSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformChainRPCTestSuite))
}

func (s *PlatformChainRPCTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformChainRPCTestSuite) TestA_Platform_AdminReplacesTheEndpointTheDialerSees() {
	s.T().Setenv("ETH_RPC_URL", "http://env-fallback.invalid/secret-env")
	token := "k" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var wantHost string
	var hit atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantHost != "" && r.Host == wantHost && strings.Contains(r.URL.Path, token) {
			hit.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x10"}`))
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	s.Require().NoError(err)
	wantHost = parsed.Host

	restore := s.installEthDialer()
	defer restore()

	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountID := s.seedOwnedAccount(admin.ID)
	before := s.loadChain(models.ChainETH)
	beforeGas := before.GasReadinessThreshold().String()
	beforeDust := deref(before.DustThresholdNativeRaw)
	beforeConfirmations := before.RequiredConfirmations

	payload, err := json.Marshal(map[string]string{"rpcUrl": server.URL + "/v2/" + token})
	s.Require().NoError(err)
	resp := s.patchRaw(session.AccessToken, "/v1/platform/chains/"+models.ChainETH+"/rpc", string(payload))
	resp.AssertOk()
	body := s.quietBody(resp, token)
	s.Equal(`{"rpcUrlSet":true}`, body)

	stored := s.loadChain(models.ChainETH)
	if stored.RpcURL == "" || stored.RpcURL == server.URL || strings.Contains(stored.RpcURL, token) {
		s.Fail("endpoint was not sealed")
	}
	if !security.IsSealedSecret(stored.RpcURL) {
		s.Fail("endpoint is not a sealed envelope")
	}
	opened, err := security.OpenSecret(facades.Crypt(), stored.RpcURL)
	s.Require().NoError(err)
	endpoint, err := models.DialEndpoint(opened)
	s.Require().NoError(err)
	openedURL, err := url.Parse(endpoint)
	s.Require().NoError(err)
	if openedURL.Host != wantHost || !strings.Contains(openedURL.Path, token) || strings.Contains(endpoint, "env-fallback") {
		s.Fail("reader did not open the stored endpoint")
	}
	s.Equal(beforeGas, stored.GasReadinessThreshold().String())
	s.Equal(beforeDust, deref(stored.DustThresholdNativeRaw))
	s.Equal(beforeConfirmations, stored.RequiredConfirmations)
	s.True(before.DustThresholdUSD.Decimal.Equal(stored.DustThresholdUSD.Decimal))

	adapter, err := container.MustMake[*chainpkg.Registry]().Chain(models.ChainETH)
	s.Require().NoError(err)
	block, err := adapter.GetLatestBlock(context.Background())
	if !hit.Load() {
		s.Fail("dialer did not call the stored endpoint")
		return
	}
	s.Require().NoError(err)
	s.Equal(uint64(16), block)

	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'chains.updated' AND target_type = 'chain' AND target_id = ?
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = '{"fields":["rpc_url"],"key":"eth"}'::jsonb`,
		models.ChainETH, admin.ID,
	))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE metadata::text LIKE ? OR metadata::text LIKE '%://%'`,
		"%"+token+"%",
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'platform.secret_viewed'`))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'chains.updated' AND account_id IS NOT NULL`,
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
}

func (s *PlatformChainRPCTestSuite) TestA_Non_AdminIsForbiddenWhateverTheChainAndAnEmptyURLIsRejected() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	before := s.loadChain(models.ChainETH).RpcURL

	forbidden := s.patchRaw(session.AccessToken, "/v1/platform/chains/eth/rpc", `{"rpcUrl":"https://dial.example/v2/not-stored"}`)
	forbidden.AssertForbidden()
	s.quietBody(forbidden, "not-stored")
	s.AssertError(forbidden, 403, responses.CodeForbidden, "you do not have permission to update chains")
	if s.loadChain(models.ChainETH).RpcURL != before {
		s.Fail("a non-admin changed the stored endpoint")
	}

	// An unknown chain is not a 404 for a member: the group guard answers first.
	missing := s.patchRaw(session.AccessToken, "/v1/platform/chains/no-such-chain/rpc", `{"rpcUrl":""}`)
	missing.AssertForbidden()
	s.AssertError(missing, 403, responses.CodeForbidden, "you do not have permission to update chains")

	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	adminSession := s.signIn(admin.Email)
	adminMissing := s.patchRaw(adminSession.AccessToken, "/v1/platform/chains/no-such-chain/rpc", `{"rpcUrl":"https://dial.example"}`)
	s.AssertError(adminMissing, 404, responses.CodeNotFound, "chain not found")
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'chains.updated'`))

	empty := s.patchRaw(adminSession.AccessToken, "/v1/platform/chains/eth/rpc", `{"rpcUrl":""}`)
	empty.AssertUnprocessableEntity()
	s.assertValidation(empty, "rpcUrl", "must not be empty")
	if s.loadChain(models.ChainETH).RpcURL != before {
		s.Fail("an empty url changed the stored endpoint")
	}
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'chains.updated'`))
}

func (s *PlatformChainRPCTestSuite) installEthDialer() func() {
	s.T().Helper()
	registry := container.MustMake[*chainpkg.Registry]()
	previous, err := registry.Chain(models.ChainETH)
	registry.RegisterChain(evmchain.NewEVMLive(evmchain.EVMConfig{
		ChainIDStr:   models.ChainETH,
		NativeSymbol: models.NativeETH,
		RPCURL:       "http://127.0.0.1:1",
	}))
	return func() {
		if err == nil && previous != nil {
			registry.RegisterChain(previous)
			return
		}
		registry.RegisterChain(evmchain.NewEVMLive(evmchain.EVMConfig{
			ChainIDStr:   models.ChainETH,
			NativeSymbol: models.NativeETH,
			RPCURL:       "http://127.0.0.1:1",
		}))
	}
}

func (s *PlatformChainRPCTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformChainRPCTestSuite) seedOwnedAccount(userID uuid.UUID) uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "rpc-" + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: models.AccountRoleOwner, Status: models.MembershipStatusActive,
	}))
	return accountID
}

func (s *PlatformChainRPCTestSuite) loadChain(id string) *models.Chain {
	s.T().Helper()
	var chain models.Chain
	s.Require().NoError(facades.Orm().Query().Where("id", id).First(&chain))
	s.Require().Equal(id, chain.ID)
	return &chain
}

func (s *PlatformChainRPCTestSuite) patchRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp := s.Patch(path, support.Session{AccessToken: token}, body)
	return resp
}

func (s *PlatformChainRPCTestSuite) quietBody(resp contractstesting.Response, secret string) string {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	if secret != "" && (strings.Contains(content, secret) || strings.Contains(content, "://")) {
		s.Fail("response included the endpoint")
	}
	return content
}

func (s *PlatformChainRPCTestSuite) accountActivity(bearer string, accountID uuid.UUID) contractstesting.Response {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String()+"/activity", support.Session{AccessToken: bearer})
	return resp
}

func (s *PlatformChainRPCTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformChainRPCTestSuite) assertValidation(resp contractstesting.Response, field, message string) {
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
