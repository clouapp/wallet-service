package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
)

// chainIDNode answers eth_chainId with chainIDHex and eth_blockNumber with 0x20,
// counting the block number calls.
func chainIDNode(t *testing.T, chainIDHex string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var blockCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		switch req.Method {
		case "eth_chainId":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"`+chainIDHex+`"}`)
		case "eth_blockNumber":
			blockCalls.Add(1)
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x20"}`)
		default:
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	return server, &blockCalls
}

func refusedURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return "http://" + addr
}

func TestRegister_ActiveChains_GivesAnEVMChainItsOpenedFallbacksInOrder(t *testing.T) {
	mainnet, mainnetCalls := chainIDNode(t, "0x1")
	sepolia, sepoliaCalls := chainIDNode(t, "0xaa36a7")
	opened := map[string]string{
		"sealed-primary": refusedURL(t),
		"sealed-mainnet": mainnet.URL,
		"sealed-sepolia": sepolia.URL,
	}
	previous := openActiveChainEndpoint
	t.Cleanup(func() { openActiveChainEndpoint = previous })
	openActiveChainEndpoint = func(stored string) (string, error) {
		if url, ok := opened[stored]; ok {
			return url, nil
		}
		return "", errors.New("open chain rpc")
	}
	sepoliaID := models.EVMNetworkIDEthereumSepolia
	rows := []models.Chain{{
		ID: models.ChainETH, Name: "Ethereum", AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH",
		NativeDecimals: 18, NetworkID: &sepoliaID, RequiredConfirmations: 12, RpcURL: "sealed-primary",
		RPCFallbacks: []models.ChainRPCEndpoint{
			{URL: "sealed-unopenable", Position: 2}, {URL: "sealed-mainnet", Position: 3}, {URL: "sealed-sepolia", Position: 4},
		},
	}}

	reg := chainpkg.NewRegistry()
	registerActiveChains(reg, rows, nil)
	eth, err := reg.Chain(models.ChainETH)
	if err != nil {
		t.Fatal(err)
	}
	block, err := eth.GetLatestBlock(context.Background())
	if err != nil || block != 0x20 {
		t.Fatalf("block=%d err=%v", block, err)
	}
	if mainnetCalls.Load() != 0 || sepoliaCalls.Load() != 1 {
		t.Fatalf("a fallback on another network served the call: mainnet=%d sepolia=%d", mainnetCalls.Load(), sepoliaCalls.Load())
	}
}

func TestOpen_ChainFallbacks_IgnoresThemForAnAdapterThatDoesNotFailOverThroughThem(t *testing.T) {
	previous := openActiveChainEndpoint
	t.Cleanup(func() { openActiveChainEndpoint = previous })
	openActiveChainEndpoint = func(stored string) (string, error) { return "https://" + strings.TrimPrefix(stored, "sealed-"), nil }

	for _, adapterType := range []string{models.AdapterTypeBitcoin, models.AdapterTypeTron, models.AdapterTypeXRP} {
		if got := openChainFallbacks(models.Chain{ID: "x", AdapterType: adapterType, RPCFallbacks: []models.ChainRPCEndpoint{{URL: "sealed-a", Position: 2}}}, ""); got != nil {
			t.Fatalf("%s: fallbacks = %v", adapterType, got)
		}
	}
	got := openChainFallbacks(models.Chain{ID: models.ChainSOL, AdapterType: models.AdapterTypeSolana, RPCFallbacks: []models.ChainRPCEndpoint{
		{URL: "sealed-a", Position: 2}, {URL: "sealed-b", Position: 3},
	}}, models.NetworkSolanaDevnet)
	if len(got) != 2 || got[0].URL != "https://a" || got[1].URL != "https://b" || got[1].Position != 3 {
		t.Fatalf("solana fallbacks = %v", got)
	}
}

func TestOpen_ChainFallbacks_KeepTheirStoredPositions(t *testing.T) {
	previous := openActiveChainEndpoint
	t.Cleanup(func() { openActiveChainEndpoint = previous })
	openActiveChainEndpoint = func(stored string) (string, error) {
		if stored == "sealed-broken" {
			return "", errors.New("open chain rpc")
		}
		return "https://" + strings.TrimPrefix(stored, "sealed-"), nil
	}

	sepoliaID := models.EVMNetworkIDEthereumSepolia
	got := openChainFallbacks(models.Chain{ID: models.ChainETH, AdapterType: models.AdapterTypeEVM, NetworkID: &sepoliaID, RPCFallbacks: []models.ChainRPCEndpoint{
		{URL: "sealed-broken", Position: 3, Enabled: true},
		{URL: "sealed-b", Position: 4, Enabled: true},
	}}, models.NetworkEthereumSepolia)
	require.Len(t, got, 1)
	assert.Equal(t, chainpkg.RPCFallback{URL: "https://b", Position: 4}, got[0])
}

func TestRegister_ActiveChains_InstallsNoFallbackWithoutANetworkToCheckThemAgainst(t *testing.T) {
	sepolia, served := chainIDNode(t, "0xaa36a7")
	previous := openActiveChainEndpoint
	t.Cleanup(func() { openActiveChainEndpoint = previous })
	opened := map[string]string{"sealed-primary": refusedURL(t), "sealed-fallback": sepolia.URL}
	openActiveChainEndpoint = func(stored string) (string, error) {
		if url, ok := opened[stored]; ok {
			return url, nil
		}
		return "", errors.New("open chain rpc")
	}
	rows := []models.Chain{
		{ID: models.ChainETH, Name: "Ethereum", AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH", NativeDecimals: 18,
			RequiredConfirmations: 12, RpcURL: "sealed-primary", RPCFallbacks: []models.ChainRPCEndpoint{{URL: "sealed-fallback", Position: 2}}},
	}

	reg := chainpkg.NewRegistry()
	registerActiveChains(reg, rows, nil)
	eth, err := reg.Chain(models.ChainETH)
	require.NoError(t, err)
	_, err = eth.GetLatestBlock(context.Background())
	assert.Error(t, err, "an EVM chain without network_id runs on its primary only")
	assert.Zero(t, served.Load())

	assert.Nil(t, openChainFallbacks(models.Chain{ID: "x", AdapterType: models.AdapterTypeEVM, RPCFallbacks: rows[0].RPCFallbacks}, ""))
	assert.Nil(t, openChainFallbacks(models.Chain{ID: models.ChainSOL, AdapterType: models.AdapterTypeSolana, RPCFallbacks: rows[0].RPCFallbacks}, ""),
		"a Solana chain whose cluster is unknown has no genesis to check against")
}
