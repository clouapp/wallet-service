package chainregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

// answeringNode answers each method with a fixed JSON result.
func answeringNode(t *testing.T, results map[string]string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		result, ok := results[req.Method]
		if !ok {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + result + `}`))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func refusedEndpoint(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return "http://" + addr + "/v2/secret-key"
}

var sepoliaTarget = EndpointTarget{Network: models.NetworkEthereumSepolia, EVMNetworkID: models.EVMNetworkIDEthereumSepolia}

func TestVerify_Endpoint_AcceptsAnEVMEndpointOnTheChainsNetworkOnly(t *testing.T) {
	ctx := context.Background()
	sepolia := answeringNode(t, map[string]string{"eth_chainId": `"0xaa36a7"`})
	served, err := VerifyEndpoint(ctx, models.AdapterTypeEVM, sepoliaTarget, sepolia)
	require.NoError(t, err)
	assert.Equal(t, models.NetworkEthereumSepolia, served)

	mainnet := answeringNode(t, map[string]string{"eth_chainId": `"0x1"`})
	served, err = VerifyEndpoint(ctx, models.AdapterTypeEVM, sepoliaTarget, mainnet)
	assert.ErrorIs(t, err, chain.ErrWrongNetwork)
	assert.Equal(t, models.NetworkEthereumMainnet, served)
	assert.Contains(t, err.Error(), "eth_chainId is 1 (ethereum-mainnet), the chain signs for 11155111 (ethereum-sepolia)")

	unreachable := refusedEndpoint(t)
	_, err = VerifyEndpoint(ctx, models.AdapterTypeEVM, sepoliaTarget, unreachable)
	assert.ErrorIs(t, err, ErrEndpointUnverified)
	assert.False(t, errors.Is(err, chain.ErrWrongNetwork))
	assert.NotContains(t, err.Error(), "secret-key")

	_, err = VerifyEndpoint(ctx, models.AdapterTypeEVM, EndpointTarget{Network: models.NetworkEthereumSepolia}, sepolia)
	assert.ErrorIs(t, err, ErrEndpointUnverified, "a chain without network_id cannot be verified")
}

func TestVerify_Endpoint_ComparesTheSolanaGenesis(t *testing.T) {
	ctx := context.Background()
	devnet := EndpointTarget{Network: models.NetworkSolanaDevnet}
	same := answeringNode(t, map[string]string{"getGenesisHash": `"` + models.SolanaGenesisHash(models.NetworkSolanaDevnet) + `"`})
	served, err := VerifyEndpoint(ctx, models.AdapterTypeSolana, devnet, same)
	require.NoError(t, err)
	assert.Equal(t, models.NetworkSolanaDevnet, served)

	mainnet := answeringNode(t, map[string]string{"getGenesisHash": `"` + models.SolanaGenesisHash(models.NetworkSolanaMainnet) + `"`})
	served, err = VerifyEndpoint(ctx, models.AdapterTypeSolana, devnet, mainnet)
	assert.ErrorIs(t, err, chain.ErrWrongNetwork)
	assert.Equal(t, models.NetworkSolanaMainnet, served)
}

func TestVerify_Endpoint_RefusesToGuessForAnAdapterWithoutAnIdentityCall(t *testing.T) {
	for _, adapterType := range []string{models.AdapterTypeBitcoin, models.AdapterTypeTron, models.AdapterTypeXRP} {
		_, err := VerifyEndpoint(context.Background(), adapterType, EndpointTarget{}, "https://example.invalid")
		assert.ErrorIs(t, err, ErrEndpointIdentityUnsupported, adapterType)
	}
}

func TestProbe_Endpoint_ReadsTheNetworkAndTheHead(t *testing.T) {
	ctx := context.Background()
	node := answeringNode(t, map[string]string{"eth_chainId": `"0xaa36a7"`, "eth_blockNumber": `"0xb62506"`})
	probe := ProbeEndpoint(ctx, models.AdapterTypeEVM, sepoliaTarget, node)
	require.NoError(t, probe.Err)
	assert.Equal(t, uint64(0xb62506), probe.Head)
	assert.Equal(t, models.NetworkEthereumSepolia, probe.Network)

	solana := answeringNode(t, map[string]string{"getGenesisHash": `"` + models.SolanaGenesisHash(models.NetworkSolanaDevnet) + `"`, "getSlot": `512232469`})
	probe = ProbeEndpoint(ctx, models.AdapterTypeSolana, EndpointTarget{Network: models.NetworkSolanaDevnet}, solana)
	require.NoError(t, probe.Err)
	assert.Equal(t, uint64(512232469), probe.Head)

	wrong := ProbeEndpoint(ctx, models.AdapterTypeEVM, sepoliaTarget, answeringNode(t, map[string]string{"eth_chainId": `"0x1"`}))
	assert.ErrorIs(t, wrong.Err, chain.ErrWrongNetwork)
	assert.Zero(t, wrong.Head)
}

func TestChain_EndpointTarget_IsTheNetworkTheRecordIsOnNow(t *testing.T) {
	sepoliaID := models.EVMNetworkIDEthereumSepolia
	eth := models.Chain{ID: models.ChainETH, AdapterType: models.AdapterTypeEVM, NetworkID: &sepoliaID, IsTestnet: true}
	assert.Equal(t, sepoliaTarget, ChainEndpointTarget(eth, ""))

	sol := models.Chain{ID: models.ChainSOL, AdapterType: models.AdapterTypeSolana, IsTestnet: true}
	assert.Equal(t, models.NetworkSolanaDevnet, ChainEndpointTarget(sol, "https://rpc.example").Network)
	assert.Equal(t, models.NetworkSolanaTestnet, ChainEndpointTarget(sol, "https://api.testnet.solana.com").Network)

	profile, ok := ProfileEndpointTarget(models.ChainNetworkProfileMainnet, eth)
	require.True(t, ok)
	assert.Equal(t, models.EVMNetworkIDEthereumMainnet, profile.EVMNetworkID)
	_, ok = ProfileEndpointTarget(models.ChainNetworkProfileMainnet, models.Chain{ID: models.ChainTETH})
	assert.False(t, ok, "the profile does not decide a t-prefixed record")
	_, ok = ProfileEndpointTarget("", eth)
	assert.False(t, ok)
	assert.True(t, strings.HasPrefix(evmNetworkLabel(42), "evm chain 42"))
}
