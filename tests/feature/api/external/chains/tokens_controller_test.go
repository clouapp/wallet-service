package chains

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestActiveTokenEndpointActualAuthAndEnvironment(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	q := facades.Orm().Query()
	testAccount, testBearer, _ := support.SetupAPIAuth(t, false)
	prodAccount, prodBearer, _ := support.SetupAPIAuth(t, false)
	require.NoError(t, repositories.NewAccountRepository(nil).SetEnvironment(ctx, testAccount, "test"))
	require.NoError(t, repositories.NewAccountRepository(nil).SetEnvironment(ctx, prodAccount, "prod"))
	for _, chain := range []models.Chain{{ID: "eth", Name: "Ethereum Sepolia", AdapterType: "evm", NativeSymbol: "eth", NativeDecimals: 18, IsTestnet: true, Status: "active", RpcURL: "secret-rpc-url"}, {ID: "sol", Name: "Solana Mainnet", AdapterType: "solana", NativeSymbol: "sol", NativeDecimals: 9, IsTestnet: false, Status: "active", RpcURL: "secret-rpc-url"}, {ID: "btc", Name: "Bitcoin Test", AdapterType: "bitcoin", NativeSymbol: "btc", NativeDecimals: 8, IsTestnet: true, Status: "active", RpcURL: "secret-rpc-url"}, {ID: "polygon", Name: "Inactive Polygon", AdapterType: "evm", NativeSymbol: "pol", NativeDecimals: 18, IsTestnet: true, Status: "inactive", RpcURL: "secret-rpc-url"}} {
		row := chain
		require.NoError(t, q.Create(&row))
	}
	for _, token := range []models.Token{{ID: uuid.New(), ChainID: "eth", Symbol: "USDC", Name: "Test USDC", ContractAddress: models.USDCContractSepolia, Decimals: 6, Status: "active"}, {ID: uuid.New(), ChainID: "eth", Symbol: "USDT", Name: "Mock USDT (Sepolia Test)", ContractAddress: "0x7169D38820dfd117C3FA1f22a697dBA58d90BA06", Decimals: 6, Status: "active"}, {ID: uuid.New(), ChainID: "eth", Symbol: "USDT", Name: "Mainnet USDT", ContractAddress: models.USDTContractETH, Decimals: 6, Status: "inactive"}, {ID: uuid.New(), ChainID: "sol", Symbol: "USDT", Name: "Inactive USDT", ContractAddress: models.USDTMintSOL, Decimals: 6, Status: "inactive"}} {
		row := token
		require.NoError(t, q.Create(&row))
	}
	call := func(chainID, bearer string) (int, []map[string]any, string) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/chains/"+chainID+"/tokens", nil)
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		request.Header.Set("X-Account-Environment", "test")
		response, requestErr := facades.Route().Test(request)
		require.NoError(t, requestErr)
		defer response.Body.Close()
		var body struct {
			Data []map[string]any `json:"data"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
		encoded, encodeErr := json.Marshal(body)
		require.NoError(t, encodeErr)
		return response.StatusCode, body.Data, string(encoded)
	}
	status, rows, _ := call("eth", testBearer)
	require.Equal(t, 200, status)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, "eth", row["chain_id"])
		require.Equal(t, "active", row["status"])
		require.Equal(t, float64(6), row["decimals"])
		require.Len(t, row, 6)
		require.NotEmpty(t, row["contract_address"])
		require.NotContains(t, row, "rpc_url")
	}
	for _, chainID := range []string{"sol", "polygon", "missing"} {
		status, _, _ = call(chainID, testBearer)
		require.Equal(t, 404, status)
	}
	status, rows, _ = call("btc", testBearer)
	require.Equal(t, 200, status)
	require.NotNil(t, rows)
	require.Empty(t, rows)
	status, _, _ = call("eth", prodBearer)
	require.Equal(t, 404, status, "caller header must not override authenticated production environment")
	status, rows, _ = call("sol", prodBearer)
	require.Equal(t, 200, status)
	require.NotNil(t, rows)
	require.Empty(t, rows, "inactive USDT must not be reported available")
	status, _, _ = call("eth", "")
	require.Equal(t, 401, status)
}
