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
		RPCFallbacks: []string{"sealed-unopenable", "sealed-mainnet", "sealed-sepolia"},
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
		if got := openChainFallbacks(models.Chain{ID: "x", AdapterType: adapterType, RPCFallbacks: []string{"sealed-a"}}); got != nil {
			t.Fatalf("%s: fallbacks = %v", adapterType, got)
		}
	}
	got := openChainFallbacks(models.Chain{ID: models.ChainSOL, AdapterType: models.AdapterTypeSolana, RPCFallbacks: []string{"sealed-a", "sealed-b"}})
	if len(got) != 2 || got[0] != "https://a" || got[1] != "https://b" {
		t.Fatalf("solana fallbacks = %v", got)
	}
}
