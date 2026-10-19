package evm

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
	"github.com/macrowallets/waas/pkg/types"
)

// countingNode answers each method with a fixed body ("result":... or
// "error":...) and counts the calls per method. A method without an answer is a
// method-not-found error.
type countingNode struct {
	server  *httptest.Server
	mu      sync.Mutex
	calls   map[string]int
	answers map[string]string
}

func newCountingNode(t *testing.T, answers map[string]string) *countingNode {
	t.Helper()
	node := &countingNode{calls: map[string]int{}, answers: answers}
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
			answer = `"error":{"code":-32601,"message":"method not found"}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,`+answer+`}`)
	}))
	t.Cleanup(node.server.Close)
	return node
}

func (n *countingNode) count(method string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls[method]
}

func closedURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return "http://" + addr
}

const (
	sepoliaChainIDResult = `"result":"0xaa36a7"`
	scannedBlockResult   = `"result":{"hash":"0x00000000000000000000000000000000000000000000000000000000000000aa","timestamp":"0x1","transactions":[{"hash":"0x01","from":"0x1111111111111111111111111111111111111111","to":"0x2222222222222222222222222222222222222222","value":"0x5"}]}`
	transferLogsResult   = `"result":[{"address":"0x1c7d4b196cb0c7b01d743fbc6116a902379c7238","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef","0x0000000000000000000000001111111111111111111111111111111111111111","0x0000000000000000000000002222222222222222222222222222222222222222"],"data":"0x64","transactionHash":"0x02","logIndex":"0x0"}]`
)

func sepoliaWithFallbacks(primary string, fallbacks ...string) *EVMLive {
	return NewEVMLive(EVMConfig{
		ChainIDStr:   models.ChainETH,
		NativeSymbol: models.NativeETH,
		NetworkID:    models.EVMNetworkIDEthereumSepolia,
		RPCURL:       primary,
		RPCFallbacks: fallbacks,
		ERC20Tokens: []types.Token{{
			ChainID: models.ChainETH, Symbol: models.SymbolUSDC, Contract: models.USDCContractSepolia, Decimals: 6,
		}},
	})
}

func TestScan_Block_ReadsTheBlockAndItsLogsFromTheEndpointThatHasTheBlock(t *testing.T) {
	lagging := newCountingNode(t, map[string]string{
		"eth_getBlockByNumber": `"result":null`,
		"eth_getLogs":          `"result":[]`,
	})
	synced := newCountingNode(t, map[string]string{
		"eth_chainId":          sepoliaChainIDResult,
		"eth_getBlockByNumber": scannedBlockResult,
		"eth_getLogs":          transferLogsResult,
	})
	adapter := sepoliaWithFallbacks(lagging.server.URL, synced.server.URL)

	transfers, err := adapter.ScanBlock(context.Background(), 0xaa)
	if err != nil {
		t.Fatalf("ScanBlock: %v", err)
	}
	if len(transfers) != 2 || transfers[1].Asset != models.SymbolUSDC {
		t.Fatalf("transfers = %+v", transfers)
	}
	if lagging.count("eth_getLogs") != 0 || synced.count("eth_getLogs") != 1 {
		t.Fatalf("the logs must come from the endpoint that returned the block: lagging=%d synced=%d",
			lagging.count("eth_getLogs"), synced.count("eth_getLogs"))
	}
}

func TestScan_Block_ThatNoEndpointHasIsAnErrorNotAnEmptyBlock(t *testing.T) {
	primary := newCountingNode(t, map[string]string{"eth_getBlockByNumber": `"result":null`})
	fallback := newCountingNode(t, map[string]string{
		"eth_chainId":          sepoliaChainIDResult,
		"eth_getBlockByNumber": `"result":null`,
	})
	adapter := sepoliaWithFallbacks(primary.server.URL, fallback.server.URL)

	transfers, err := adapter.ScanBlock(context.Background(), 0xaa)
	if err == nil || !strings.Contains(err.Error(), "no endpoint returned block 170") || transfers != nil {
		t.Fatalf("transfers=%v err=%v", transfers, err)
	}
	if primary.count("eth_getLogs") != 0 || fallback.count("eth_getLogs") != 0 {
		t.Fatal("the logs of a block no endpoint has were read")
	}
}

func TestScan_Block_FailsInsteadOfReadingLogsFromAnotherNode(t *testing.T) {
	primary := newCountingNode(t, map[string]string{
		"eth_getBlockByNumber": scannedBlockResult,
		"eth_getLogs":          `"error":{"code":-32603,"message":"internal error"}`,
	})
	fallback := newCountingNode(t, map[string]string{
		"eth_chainId":          sepoliaChainIDResult,
		"eth_getBlockByNumber": scannedBlockResult,
		"eth_getLogs":          `"result":[]`,
	})
	adapter := sepoliaWithFallbacks(primary.server.URL, fallback.server.URL)

	if _, err := adapter.ScanBlock(context.Background(), 0xaa); err == nil {
		t.Fatal("expected the block to fail so the scanner retries it")
	}
	if fallback.count("eth_getLogs") != 0 {
		t.Fatal("the logs of a block read from the primary were asked of the fallback")
	}
}

func TestReceipt_Lookup_AsksTheNextEndpointWhenTheFirstHasNone(t *testing.T) {
	receipt := `"result":{"blockNumber":"0xb62506","status":"0x1","gasUsed":"0x5208"}`
	primary := newCountingNode(t, map[string]string{"eth_getTransactionReceipt": `"result":null`})
	fallback := newCountingNode(t, map[string]string{"eth_chainId": sepoliaChainIDResult, "eth_getTransactionReceipt": receipt})
	adapter := sepoliaWithFallbacks(primary.server.URL, fallback.server.URL)

	block, err := adapter.GetTransactionBlock(context.Background(), "0xabc")
	if err != nil || block != 0xb62506 {
		t.Fatalf("block=%d err=%v", block, err)
	}
}

func TestFallback_OnAnotherChainIDNeverServes(t *testing.T) {
	mainnet := newCountingNode(t, map[string]string{"eth_chainId": `"result":"0x1"`, "eth_blockNumber": `"result":"0x99"`})
	adapter := sepoliaWithFallbacks(closedURL(t), mainnet.server.URL)

	if _, err := adapter.GetLatestBlock(context.Background()); err == nil {
		t.Fatal("expected the primary's failure: the only fallback serves mainnet")
	}
	if mainnet.count("eth_blockNumber") != 0 {
		t.Fatal("a fallback on another network served a call")
	}
}

func TestBroadcast_AlreadyKnownOnTheFallbackReturnsTheSignedHash(t *testing.T) {
	fallback := newCountingNode(t, map[string]string{
		"eth_chainId":            sepoliaChainIDResult,
		"eth_sendRawTransaction": `"error":{"code":-32000,"message":"already known"}`,
	})
	adapter := sepoliaWithFallbacks(closedURL(t), fallback.server.URL)

	hash, err := adapter.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: []byte{0x01}, TxHash: "0xsigned"})
	if err != nil || hash != "0xsigned" {
		t.Fatalf("hash=%q err=%v", hash, err)
	}
}

func TestReplace_Endpoints_PointsTheAdapterAtTheNewList(t *testing.T) {
	fallback := newCountingNode(t, map[string]string{"eth_chainId": sepoliaChainIDResult, "eth_blockNumber": `"result":"0x20"`})
	adapter := sepoliaWithFallbacks(closedURL(t))
	primary := adapter.Endpoint()

	adapter.ReplaceEndpoints(primary, []string{fallback.server.URL})
	block, err := adapter.GetLatestBlock(context.Background())
	if err != nil || block != 0x20 || adapter.Endpoint() != primary {
		t.Fatalf("block=%d err=%v", block, err)
	}
}
