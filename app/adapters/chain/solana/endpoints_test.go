package solana

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// countingSolanaNode answers each method with a fixed body ("result":... or
// "error":...) and counts the calls per method.
type countingSolanaNode struct {
	server  *httptest.Server
	mu      sync.Mutex
	calls   map[string]int
	answers map[string]string
}

func newCountingSolanaNode(t *testing.T, answers map[string]string) *countingSolanaNode {
	t.Helper()
	node := &countingSolanaNode{calls: map[string]int{}, answers: answers}
	node.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		node.mu.Lock()
		node.calls[req.Method]++
		answer, ok := node.answers[req.Method]
		node.mu.Unlock()
		if !ok {
			answer = `"error":{"code":-32601,"message":"Method not found"}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,`+answer+`}`)
	}))
	t.Cleanup(node.server.Close)
	return node
}

func (n *countingSolanaNode) count(method string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls[method]
}

func refusedSolanaURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return "http://" + addr
}

func devnetWithFallbacks(primary string, fallbacks ...string) *SolanaLive {
	return NewSolanaLive(SolanaConfig{
		ChainIDStr:   models.ChainSOL,
		NativeSymbol: models.NativeSOL,
		RPCURL:       primary,
		RPCFallbacks: fallbackList(fallbacks),
		GenesisHash:  models.SolanaGenesisHash(models.NetworkSolanaDevnet),
	})
}

var devnetGenesisResult = `"result":"` + models.SolanaGenesisHash(models.NetworkSolanaDevnet) + `"`

func TestScan_Block_SlotNoEndpointReturnsIsAnErrorNotAnEmptySlot(t *testing.T) {
	primary := newCountingSolanaNode(t, map[string]string{"getBlock": `"result":null`})
	fallback := newCountingSolanaNode(t, map[string]string{"getGenesisHash": devnetGenesisResult, "getBlock": `"result":null`})
	adapter := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

	transfers, err := adapter.ScanBlock(context.Background(), 512232469)
	if err == nil || !strings.Contains(err.Error(), "no endpoint returned slot 512232469") || transfers != nil {
		t.Fatalf("transfers=%v err=%v", transfers, err)
	}
	if primary.count("getBlock") != 1 || fallback.count("getBlock") != 1 {
		t.Fatalf("getBlock primary=%d fallback=%d, want each asked once", primary.count("getBlock"), fallback.count("getBlock"))
	}
}

func TestScan_Block_SkippedSlotIsStillAnEmptySlotWithoutAskingTheFallback(t *testing.T) {
	primary := newCountingSolanaNode(t, map[string]string{"getBlock": `"error":{"code":-32007,"message":"Slot 1 was skipped"}`})
	fallback := newCountingSolanaNode(t, map[string]string{"getGenesisHash": devnetGenesisResult})
	adapter := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

	transfers, err := adapter.ScanBlock(context.Background(), 1)
	if err != nil || transfers != nil || fallback.count("getBlock") != 0 {
		t.Fatalf("transfers=%v err=%v fallback=%d", transfers, err, fallback.count("getBlock"))
	}
}

func TestFallback_OnAnotherClusterNeverServes(t *testing.T) {
	mainnet := newCountingSolanaNode(t, map[string]string{
		"getGenesisHash": `"result":"` + models.SolanaGenesisHash(models.NetworkSolanaMainnet) + `"`,
		"getSlot":        `"result":42`,
	})
	adapter := devnetWithFallbacks(refusedSolanaURL(t), mainnet.server.URL)

	if _, err := adapter.GetLatestBlock(context.Background()); err == nil {
		t.Fatal("expected the primary's failure: the only fallback is mainnet")
	}
	if mainnet.count("getSlot") != 0 {
		t.Fatal("a fallback on another cluster served a call")
	}
}

func TestBroadcast_AlreadyProcessedOnTheFallbackReturnsTheSignature(t *testing.T) {
	fallback := newCountingSolanaNode(t, map[string]string{
		"getGenesisHash":  devnetGenesisResult,
		"sendTransaction": `"error":{"code":-32002,"message":"Transaction simulation failed: This transaction has already been processed"}`,
	})
	adapter := devnetWithFallbacks(refusedSolanaURL(t), fallback.server.URL)

	signature, err := adapter.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: []byte{0x01}, TxHash: "5sig"})
	if err != nil || signature != "5sig" {
		t.Fatalf("signature=%q err=%v", signature, err)
	}
}

// fallbackList is urls as fallbacks that take their place in the list as position.
func fallbackList(urls []string) []chain.RPCFallback {
	fallbacks := make([]chain.RPCFallback, 0, len(urls))
	for _, url := range urls {
		fallbacks = append(fallbacks, chain.RPCFallback{URL: url})
	}
	return fallbacks
}
