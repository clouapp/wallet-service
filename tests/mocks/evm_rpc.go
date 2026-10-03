package mocks

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// JSON-RPC error code geth returns when eth_estimateGas / eth_call revert.
const FakeEVMRevertCode = 3

// FakeEVMCall is one JSON-RPC request received by FakeEVMNode.
type FakeEVMCall struct {
	Method string
	Params []json.RawMessage
}

// FakeEVMGasPriceOracle is the OP-stack GasPriceOracle predeploy; eth_call to it
// answers L1FeeHex.
const FakeEVMGasPriceOracle = "0x420000000000000000000000000000000000000f"

// FakeEVMNode is an in-process EVM JSON-RPC endpoint for adapter tests.
// Hex fields are returned verbatim as results. When EstimateGasError is set,
// eth_estimateGas answers with a revert error instead of EstimateGasHex.
type FakeEVMNode struct {
	GasPriceHex      string
	NonceHex         string
	EstimateGasHex   string
	EstimateGasError string
	NativeBalanceHex string
	TokenBalanceHex  string
	L1FeeHex         string
	SendRawTxHash    string

	mu     sync.Mutex
	calls  []FakeEVMCall
	server *httptest.Server
}

// NewFakeEVMNode starts the fake node and closes it when the test ends.
func NewFakeEVMNode(t *testing.T) *FakeEVMNode {
	t.Helper()
	node := &FakeEVMNode{
		GasPriceHex:      "0x3b9aca00", // 1 gwei
		NonceHex:         "0x7",
		NativeBalanceHex: "0x0",
		TokenBalanceHex:  "0x0",
		SendRawTxHash:    "0x" + fmt.Sprintf("%064x", 0xabc),
	}
	node.server = httptest.NewServer(http.HandlerFunc(node.handle))
	t.Cleanup(node.server.Close)
	return node
}

// URL is the RPC endpoint to pass to the adapter config.
func (n *FakeEVMNode) URL() string { return n.server.URL }

// Calls returns the requests received so far, oldest first.
func (n *FakeEVMNode) Calls() []FakeEVMCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]FakeEVMCall(nil), n.calls...)
}

// CallsTo returns only the requests for `method`.
func (n *FakeEVMNode) CallsTo(method string) []FakeEVMCall {
	matching := make([]FakeEVMCall, 0)
	for _, call := range n.Calls() {
		if call.Method == method {
			matching = append(matching, call)
		}
	}
	return matching
}

func (n *FakeEVMNode) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     uint64            `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n.mu.Lock()
	n.calls = append(n.calls, FakeEVMCall{Method: req.Method, Params: req.Params})
	n.mu.Unlock()

	result, rpcErr := n.answer(req.Method, req.Params)
	body := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
	if rpcErr != nil {
		body["error"] = rpcErr
	} else {
		body["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// CallsToContract returns the eth_call requests sent to contract.
func (n *FakeEVMNode) CallsToContract(contract string) []FakeEVMCall {
	matching := make([]FakeEVMCall, 0)
	for _, call := range n.CallsTo("eth_call") {
		if strings.EqualFold(ethCallTarget(call.Params), contract) {
			matching = append(matching, call)
		}
	}
	return matching
}

func ethCallTarget(params []json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var call struct {
		To string `json:"to"`
	}
	if err := json.Unmarshal(params[0], &call); err != nil {
		return ""
	}
	return call.To
}

func (n *FakeEVMNode) answer(method string, params []json.RawMessage) (string, map[string]interface{}) {
	switch method {
	case "eth_call":
		if strings.EqualFold(ethCallTarget(params), FakeEVMGasPriceOracle) {
			if n.L1FeeHex == "" {
				return "", map[string]interface{}{"code": FakeEVMRevertCode, "message": "execution reverted: no GasPriceOracle"}
			}
			return n.L1FeeHex, nil
		}
		return n.TokenBalanceHex, nil
	case "eth_gasPrice":
		return n.GasPriceHex, nil
	case "eth_getTransactionCount":
		return n.NonceHex, nil
	case "eth_estimateGas":
		if n.EstimateGasError != "" {
			return "", map[string]interface{}{"code": FakeEVMRevertCode, "message": n.EstimateGasError}
		}
		return n.EstimateGasHex, nil
	case "eth_getBalance":
		return n.NativeBalanceHex, nil
	case "eth_sendRawTransaction":
		return n.SendRawTxHash, nil
	default:
		return "", map[string]interface{}{"code": -32601, "message": "method not found: " + method}
	}
}
