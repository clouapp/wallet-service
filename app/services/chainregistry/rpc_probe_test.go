package chainregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func TestBitcoinNetworkOfRPCURLReadsTheEsploraPath(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"https://blockstream.info/api":          models.NetworkBitcoinMainnet,
		"https://blockstream.info/testnet/api":  models.NetworkBitcoinTestnet,
		"https://mempool.space/testnet/api/":    models.NetworkBitcoinTestnet,
		"https://mempool.space/testnet4/api":    models.NetworkBitcoinTestnet4,
		"https://mempool.space/signet/api":      NetworkBitcoinSignet,
		"https://mempool.space/api":             models.NetworkBitcoinMainnet,
		"https://bitcoind.internal:8332":        "",
		"https://evil-blockstream.info/testnet": "",
		"not a url":                             "",
	}
	for rpcURL, want := range cases {
		assert.Equal(t, want, BitcoinNetworkOfRPCURL(rpcURL), rpcURL)
	}
}

func evmNode(t *testing.T, chainIDHex string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, evmChainIDMethod, req.Method)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + chainIDHex + `"}`))
	}))
}

func TestProbeAsksAnEVMNodeForItsChainID(t *testing.T) {
	sepolia := evmNode(t, "0xaa36a7")
	defer sepolia.Close()
	unknown := evmNode(t, "0x2a")
	defer unknown.Close()
	evm := models.Chain{AdapterType: models.AdapterTypeEVM}
	ctx := context.Background()

	network, err := ProbeRPCNetwork(ctx, evm, sepolia.URL)
	require.NoError(t, err)
	assert.Equal(t, models.NetworkEthereumSepolia, network)

	network, err = ProbeRPCNetwork(ctx, evm, unknown.URL)
	require.NoError(t, err)
	assert.Empty(t, network)
}

func TestProbeFailsWhenTheEVMNodeAnswersGarbage(t *testing.T) {
	garbage := evmNode(t, "0xzz")
	defer garbage.Close()

	_, err := ProbeRPCNetwork(context.Background(), models.Chain{AdapterType: models.AdapterTypeEVM}, garbage.URL)

	assert.Error(t, err)
}

func TestProbeReadsSolanaAndBitcoinFromTheURL(t *testing.T) {
	ctx := context.Background()
	network, err := ProbeRPCNetwork(ctx, models.Chain{AdapterType: models.AdapterTypeSolana}, "https://api.devnet.solana.com")
	require.NoError(t, err)
	assert.Equal(t, models.NetworkSolanaDevnet, network)

	network, err = ProbeRPCNetwork(ctx, models.Chain{AdapterType: models.AdapterTypeBitcoin}, "https://blockstream.info/testnet/api")
	require.NoError(t, err)
	assert.Equal(t, models.NetworkBitcoinTestnet, network)

	network, err = ProbeRPCNetwork(ctx, models.Chain{AdapterType: "unknown"}, "https://example.com")
	require.NoError(t, err)
	assert.Empty(t, network)
}
