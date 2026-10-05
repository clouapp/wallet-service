package deposit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	evmchain "github.com/macrowallets/waas/app/adapters/chain/evm"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/blockheight"
)

type failingTipProvider struct{ asked int }

func (p *failingTipProvider) GetBlockHeight(context.Context, string) (uint64, error) {
	p.asked++
	return 0, context.DeadlineExceeded
}

func headNode(t *testing.T, headHex string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID uint64 `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": headHex})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestChainRPCNetworksReadTheTipFromTheAdapterOnTheFirstTick(t *testing.T) {
	adapter := evmchain.NewEVMLive(evmchain.EVMConfig{ChainIDStr: models.ChainBase, NetworkID: models.EVMNetworkIDBaseSepolia, RPCURL: headNode(t, "0x2d6f3c7").URL})
	inner := &failingTipProvider{}
	svc := &Service{
		blockHeightProviders: map[string]blockheight.Provider{
			models.AdapterTypeEVM: blockheight.RouteByNetwork(inner, map[string]string{models.ChainBase: models.NetworkBaseSepolia}),
		},
		heightFailures: make(map[string]int),
	}

	height, ok := svc.resolveCurrentBlockHeight(context.Background(), models.ChainBase, adapter)
	if !ok || height != 0x2d6f3c7 {
		t.Fatalf("height %d ok=%t, want the chain RPC head without waiting for provider failures", height, ok)
	}
	if inner.asked != 0 || svc.heightFailures[models.ChainBase] != 0 {
		t.Fatalf("the provider must not be asked (asked %d, failures %d)", inner.asked, svc.heightFailures[models.ChainBase])
	}
}

func TestProviderNetworksStillWaitForRepeatedFailuresBeforeFallingBack(t *testing.T) {
	adapter := evmchain.NewEVMLive(evmchain.EVMConfig{ChainIDStr: models.ChainETH, NetworkID: models.EVMNetworkIDEthereumSepolia, RPCURL: headNode(t, "0x10").URL})
	inner := &failingTipProvider{}
	svc := &Service{
		blockHeightProviders: map[string]blockheight.Provider{
			models.AdapterTypeEVM: blockheight.RouteByNetwork(inner, map[string]string{models.ChainETH: models.NetworkEthereumSepolia}),
		},
		heightFailures: make(map[string]int),
	}

	if _, ok := svc.resolveCurrentBlockHeight(context.Background(), models.ChainETH, adapter); ok {
		t.Fatal("a first provider failure on a provider-served network must not fall back yet")
	}
	if inner.asked != 1 {
		t.Fatalf("provider asked %d times, want 1", inner.asked)
	}
}
