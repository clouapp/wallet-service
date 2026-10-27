package chains

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

type tokenChains struct {
	row   *models.Chain
	reads int
	err   error
}

func (c *tokenChains) FindActive(context.Context) ([]models.Chain, error) {
	panic("unexpected chain list")
}
func (c *tokenChains) FindByTestnet(context.Context, bool) ([]models.Chain, error) {
	panic("unexpected chain list")
}
func (c *tokenChains) FindByID(context.Context, string) (*models.Chain, error) {
	c.reads++
	return c.row, c.err
}

type tokenRows struct {
	rows  []models.Token
	reads int
	err   error
}

func (t *tokenRows) FindByChainID(context.Context, string) ([]models.Token, error) {
	t.reads++
	return t.rows, t.err
}
func tokenHTTPContext(environment string) (http.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	g, _ := gin.CreateTestContext(rec)
	g.Request = httptest.NewRequest("GET", "/api/v1/chains/eth/tokens", nil)
	g.Params = gin.Params{{Key: "chainId", Value: "eth"}}
	ctx := ginpkg.NewContext(g)
	if environment != "" {
		ctx.WithValue(requestctx.KeyAccountEnvironment, environment)
	}
	return ctx, rec
}
func TestExternalTokenCatalogFailsClosedBeforeReadingTokens(t *testing.T) {
	for _, tc := range []struct {
		name, environment, status string
		testnet                   bool
		chainErr                  error
		expected                  int
	}{
		{name: "missing environment", status: "active", testnet: true, expected: 403},
		{name: "unknown environment", environment: "invalid", status: "active", testnet: true, expected: 403},
		{name: "production requesting testnet", environment: "prod", status: "active", testnet: true, expected: 404},
		{name: "test requesting mainnet", environment: "test", status: "active", expected: 404},
		{name: "inactive", environment: "test", status: "inactive", testnet: true, expected: 404},
		{name: "missing chain", environment: "test", chainErr: models.ErrRepositoryNotFound, expected: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chains := &tokenChains{row: &models.Chain{ID: "eth", Status: tc.status, IsTestnet: tc.testnet}, err: tc.chainErr}
			tokens := &tokenRows{}
			ctl := NewChainController(chainsvc.NewService(chainsvc.Deps{Chains: chains, Tokens: tokens}))
			ctx, rec := tokenHTTPContext(tc.environment)
			require.NoError(t, ctl.Tokens(ctx).Render())
			require.Equal(t, tc.expected, rec.Code)
			require.Zero(t, tokens.reads)
			if tc.environment == "" || tc.environment == "invalid" {
				require.Zero(t, chains.reads)
			}
		})
	}
}
func TestExternalTokenCatalogOnlyExposesActiveOwnChainMetadata(t *testing.T) {
	chains := &tokenChains{row: &models.Chain{ID: "eth", Status: "active", IsTestnet: true, RpcURL: "secret-rpc-url"}}
	tokens := &tokenRows{rows: []models.Token{{ChainID: "eth", Symbol: "USDT", Name: "Mock USDT (Sepolia Test)", ContractAddress: "0x7169D38820dfd117C3FA1f22a697dBA58d90BA06", Decimals: 6, Status: "active"}, {ChainID: "eth", Symbol: "USDT", ContractAddress: models.USDTContractETH, Decimals: 6, Status: "inactive"}, {ChainID: "sol", Symbol: "USDC", Decimals: 6, Status: "active"}}}
	ctl := NewChainController(chainsvc.NewService(chainsvc.Deps{Chains: chains, Tokens: tokens}))
	ctx, rec := tokenHTTPContext("test")
	require.NoError(t, ctl.Tokens(ctx).Render())
	require.Equal(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), "secret-rpc-url")
	require.NotContains(t, rec.Body.String(), "rpc_url")
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data, 1)
	require.Equal(t, map[string]any{"chain_id": "eth", "symbol": "USDT", "name": "Mock USDT (Sepolia Test)", "contract_address": "0x7169D38820dfd117C3FA1f22a697dBA58d90BA06", "decimals": float64(6), "status": "active"}, payload.Data[0])
}
func TestExternalTokenCatalogOutageIsNotAnEmptySupportedRegistry(t *testing.T) {
	chains := &tokenChains{row: &models.Chain{ID: "eth", Status: "active", IsTestnet: true}}
	tokens := &tokenRows{err: errors.New("secret-rpc-url")}
	ctl := NewChainController(chainsvc.NewService(chainsvc.Deps{Chains: chains, Tokens: tokens}))
	ctx, rec := tokenHTTPContext("test")
	require.NoError(t, ctl.Tokens(ctx).Render())
	require.Equal(t, 500, rec.Code)
	require.False(t, strings.Contains(rec.Body.String(), "secret-rpc-url"))
}
