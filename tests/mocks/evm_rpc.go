package mocks

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// JSON-RPC error code geth returns when eth_estimateGas / eth_call revert.
const FakeEVMRevertCode = 3

// FakeEVMInsufficientFundsCode is the JSON-RPC error code geth answers an
// eth_estimateGas whose sender cannot pay its value with.
const FakeEVMInsufficientFundsCode = -32000

// FakeEVMCall is one JSON-RPC request received by FakeEVMNode.
type FakeEVMCall struct {
	Method string
	Params []json.RawMessage
}

// FakeEVMGasPriceOracle is the OP-stack GasPriceOracle predeploy; eth_call to it
// answers L1FeeHex.
const FakeEVMGasPriceOracle = "0x420000000000000000000000000000000000000f"

// FakeEVMNativeTransferGasHex is what a plain transfer to an existing account
// costs: 21000 gas.
const FakeEVMNativeTransferGasHex = "0x5208"

// FakeEVMReceipt is a mined (Block > 0) or pending (Block == 0) transaction the
// fake answers eth_getTransactionByHash and eth_getTransactionReceipt with.
type FakeEVMReceipt struct {
	Block uint64
	// Status is the receipt status: "0x1" succeeded, "0x0" failed (out of gas or reverted).
	Status string
	// Gas is the transaction's gas limit; GasUsed is what the receipt reports.
	Gas               uint64
	GasUsed           uint64
	EffectiveGasPrice uint64
}

// FakeEVMEstimate is the transaction an eth_estimateGas call asked about.
type FakeEVMEstimate struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"`
	Data  string `json:"data"`
}

// FakeEVMNode is an in-process EVM JSON-RPC endpoint for adapter tests.
// Hex fields are returned verbatim as results. When EstimateGasError is set,
// every eth_estimateGas answers with a revert error.
//
// A contract call (an eth_estimateGas with data) answers EstimateGasHex. A
// native transfer (no data) is answered like a network would: NativeTransferGasHex
// (default 21000) for a transfer to an existing account and, when
// AccountCreationGasHex is set, AccountCreationGasHex for a value-carrying
// transfer to a recipient not listed in ExistingAccounts (a network that prices
// state creation: Sepolia answered 0x32a1f on 2026-10-18).
type FakeEVMNode struct {
	GasPriceHex      string
	NonceHex         string
	EstimateGasHex   string
	EstimateGasError string
	NativeBalanceHex string
	TokenBalanceHex  string
	L1FeeHex         string
	SendRawTxHash    string
	BlockNumberHex   string

	NativeTransferGasHex  string
	AccountCreationGasHex string
	ExistingAccounts      []string
	// EnforceSenderBalance makes a native estimate whose sender holds less than
	// its value (NativeBalanceHex) fail as geth does. A call without from uses the
	// node's default sender, which always covers it.
	EnforceSenderBalance bool
	// EstimateGasHTTPStatus, when set, answers eth_estimateGas with that HTTP
	// status instead of a JSON-RPC body (a provider outage).
	EstimateGasHTTPStatus int
	// Receipts answers eth_getTransactionByHash and eth_getTransactionReceipt by
	// lowercase hash; an unknown hash is null.
	Receipts map[string]FakeEVMReceipt

	mu     sync.Mutex
	calls  []FakeEVMCall
	server *httptest.Server
}

// NewFakeEVMNode starts the fake node and closes it when the test ends.
func NewFakeEVMNode(t *testing.T) *FakeEVMNode {
	t.Helper()
	node := &FakeEVMNode{
		GasPriceHex:          "0x3b9aca00", // 1 gwei
		NonceHex:             "0x7",
		NativeBalanceHex:     "0x0",
		TokenBalanceHex:      "0x0",
		SendRawTxHash:        "0x" + fmt.Sprintf("%064x", 0xabc),
		BlockNumberHex:       "0x64",
		NativeTransferGasHex: FakeEVMNativeTransferGasHex,
		Receipts:             map[string]FakeEVMReceipt{},
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

// Estimates decodes the eth_estimateGas calls received so far, oldest first.
func (n *FakeEVMNode) Estimates(t *testing.T) []FakeEVMEstimate {
	t.Helper()
	calls := n.CallsTo("eth_estimateGas")
	estimates := make([]FakeEVMEstimate, 0, len(calls))
	for _, call := range calls {
		estimate, err := decodeEstimate(call.Params)
		if err != nil {
			t.Fatalf("decode eth_estimateGas params: %v", err)
		}
		estimates = append(estimates, estimate)
	}
	return estimates
}

func decodeEstimate(params []json.RawMessage) (FakeEVMEstimate, error) {
	var estimate FakeEVMEstimate
	if len(params) == 0 {
		return estimate, nil
	}
	err := json.Unmarshal(params[0], &estimate)
	return estimate, err
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

	if req.Method == "eth_estimateGas" && n.EstimateGasHTTPStatus != 0 {
		http.Error(w, "upstream unavailable", n.EstimateGasHTTPStatus)
		return
	}
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

func (n *FakeEVMNode) answer(method string, params []json.RawMessage) (interface{}, map[string]interface{}) {
	switch method {
	case "eth_call":
		if strings.EqualFold(ethCallTarget(params), FakeEVMGasPriceOracle) {
			if n.L1FeeHex == "" {
				return nil, map[string]interface{}{"code": FakeEVMRevertCode, "message": "execution reverted: no GasPriceOracle"}
			}
			return n.L1FeeHex, nil
		}
		return n.TokenBalanceHex, nil
	case "eth_gasPrice":
		return n.GasPriceHex, nil
	case "eth_getTransactionCount":
		return n.NonceHex, nil
	case "eth_estimateGas":
		return n.estimateGas(params)
	case "eth_getBalance":
		return n.NativeBalanceHex, nil
	case "eth_sendRawTransaction":
		return n.SendRawTxHash, nil
	case "eth_blockNumber":
		return n.BlockNumberHex, nil
	case "eth_getTransactionByHash":
		return n.transactionByHash(params), nil
	case "eth_getTransactionReceipt":
		return n.transactionReceipt(params), nil
	default:
		return nil, map[string]interface{}{"code": -32601, "message": "method not found: " + method}
	}
}

func (n *FakeEVMNode) estimateGas(params []json.RawMessage) (interface{}, map[string]interface{}) {
	if n.EstimateGasError != "" {
		return nil, map[string]interface{}{"code": FakeEVMRevertCode, "message": n.EstimateGasError}
	}
	call, err := decodeEstimate(params)
	if err != nil {
		return nil, map[string]interface{}{"code": -32602, "message": "invalid params: " + err.Error()}
	}
	if strings.TrimPrefix(call.Data, "0x") != "" {
		return n.EstimateGasHex, nil
	}
	value := hexQuantity(call.Value)
	if n.EnforceSenderBalance && call.From != "" && hexQuantity(n.NativeBalanceHex).Cmp(value) < 0 {
		return nil, map[string]interface{}{"code": FakeEVMInsufficientFundsCode, "message": "insufficient funds for transfer"}
	}
	if n.AccountCreationGasHex != "" && value.Sign() > 0 && !n.accountExists(call.To) {
		return n.AccountCreationGasHex, nil
	}
	return n.NativeTransferGasHex, nil
}

func (n *FakeEVMNode) accountExists(address string) bool {
	for _, existing := range n.ExistingAccounts {
		if strings.EqualFold(existing, address) {
			return true
		}
	}
	return false
}

func (n *FakeEVMNode) receipt(params []json.RawMessage) (string, FakeEVMReceipt, bool) {
	if len(params) == 0 {
		return "", FakeEVMReceipt{}, false
	}
	var hash string
	if err := json.Unmarshal(params[0], &hash); err != nil {
		return "", FakeEVMReceipt{}, false
	}
	receipt, ok := n.Receipts[strings.ToLower(hash)]
	return hash, receipt, ok
}

func (n *FakeEVMNode) transactionByHash(params []json.RawMessage) interface{} {
	hash, receipt, ok := n.receipt(params)
	if !ok {
		return nil
	}
	tx := map[string]interface{}{
		"hash":        hash,
		"gas":         hexOf(receipt.Gas),
		"gasPrice":    hexOf(receipt.EffectiveGasPrice),
		"blockNumber": nil,
	}
	if receipt.Block > 0 {
		tx["blockNumber"] = hexOf(receipt.Block)
	}
	return tx
}

func (n *FakeEVMNode) transactionReceipt(params []json.RawMessage) interface{} {
	hash, receipt, ok := n.receipt(params)
	if !ok || receipt.Block == 0 {
		return nil
	}
	return map[string]interface{}{
		"transactionHash":   hash,
		"blockNumber":       hexOf(receipt.Block),
		"status":            receipt.Status,
		"gasUsed":           hexOf(receipt.GasUsed),
		"effectiveGasPrice": hexOf(receipt.EffectiveGasPrice),
	}
}

func hexOf(value uint64) string {
	return fmt.Sprintf("0x%x", value)
}

func hexQuantity(value string) *big.Int {
	digits := strings.TrimPrefix(strings.TrimSpace(value), "0x")
	parsed, ok := new(big.Int).SetString(digits, 16)
	if digits == "" || !ok {
		return new(big.Int)
	}
	return parsed
}
