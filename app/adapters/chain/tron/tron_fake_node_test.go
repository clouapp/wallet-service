package tron

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

// Live Nile/mainnet parameters (October 2026).
var tronLiveChainParams = map[string]int64{
	tronParamTransactionFee:      1_000,
	tronParamEnergyFee:           100,
	tronParamCreateAccountFee:    100_000,
	tronParamCreateNewAccountFee: 1_000_000,
	tronParamMaxFeeLimit:         15_000_000_000,
	"getFreeNetLimit":            600,
}

const (
	tronTestHeadNumber    = int64(71532773)
	tronTestHeadBlockID   = "00000000044380e58a4462a4d528c77ed871164034c42047cdbecfce85ca8ce0"
	tronTestHeadTimestamp = int64(1791126834000)
	tronTestNow           = int64(1791126835123)
	// A contract deployer, its per-call energy cap and the energy it has used.
	tronTestOriginHex         = "4166b9c0ab3a5a1a1a3d8a8e0a7d6a1a2a3b4c5d6e"
	tronTestOriginEnergyLimit = int64(1_000_000_000)
	tronTestOriginEnergyUsed  = int64(289_084_914)
)

var tronTestNileUSDT = types.Token{Symbol: models.SymbolUSDT, Name: "Tether USD", Contract: models.USDTContractTronNile, Decimals: 6, ChainID: models.ChainTron}

// fakeTronNode answers the read-only java-tron HTTP API from in-memory state. Any
// other path — broadcasthex included — fails the test.
type fakeTronNode struct {
	t      *testing.T
	mu     sync.Mutex
	params map[string]int64
	// accounts maps hex addresses (41…) to TRX balances; absent = never activated.
	accounts map[string]int64
	// tokenBalances maps hex holder addresses to balanceOf results.
	tokenBalances map[string]*big.Int
	// energyRequired is estimateenergy's answer; 0 answers like TronGrid mainnet
	// ("this node does not support estimate energy").
	energyRequired int64
	energyUsed     int64
	// contractUserPercent, originEnergyLimit and originEnergyLeft describe every
	// contract's energy split (getcontract / the deployer's getaccountresource).
	contractUserPercent int64
	originEnergyLimit   int64
	originEnergyLeft    int64
	revertTransfer      bool
	blocks              map[uint64]json.RawMessage
	infos               map[uint64]json.RawMessage
	txInfos             map[string]string
	failPaths           map[string]int
	calls               map[string]int
	lastCalls           map[string]map[string]any
}

func newFakeTronNode(t *testing.T) *fakeTronNode {
	t.Helper()
	params := make(map[string]int64, len(tronLiveChainParams))
	for key, value := range tronLiveChainParams {
		params[key] = value
	}
	return &fakeTronNode{
		t:                   t,
		params:              params,
		accounts:            map[string]int64{},
		tokenBalances:       map[string]*big.Int{},
		energyRequired:      21_975,
		energyUsed:          14_650,
		contractUserPercent: tronFullUserResourcePercent,
		originEnergyLimit:   tronTestOriginEnergyLimit,
		blocks:              map[uint64]json.RawMessage{},
		infos:               map[uint64]json.RawMessage{},
		txInfos:             map[string]string{},
		failPaths:           map[string]int{},
		calls:               map[string]int{},
		lastCalls:           map[string]map[string]any{},
	}
}

func (f *fakeTronNode) fund(t *testing.T, address string, sun int64) {
	t.Helper()
	f.accounts[mustTronHex(t, address)] = sun
}

func (f *fakeTronNode) holdTokens(t *testing.T, address string, amount int64) {
	t.Helper()
	f.tokenBalances[mustTronHex(t, address)] = big.NewInt(amount)
}

func (f *fakeTronNode) callCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func (f *fakeTronNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	f.calls[r.URL.Path]++
	f.lastCalls[r.URL.Path] = req
	if status, ok := f.failPaths[r.URL.Path]; ok {
		http.Error(w, "injected failure", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/wallet/getchainparameters":
		params := make([]map[string]any, 0, len(f.params))
		for key, value := range f.params {
			params = append(params, map[string]any{"key": key, "value": value})
		}
		writeJSON(w, map[string]any{"chainParameter": params})
	case "/wallet/getaccount":
		address, _ := req["address"].(string)
		balance, ok := f.accounts[strings.ToLower(address)]
		if !ok {
			_, _ = io.WriteString(w, "{}")
			return
		}
		account := map[string]any{"address": address, "create_time": 1603697244000}
		if balance > 0 {
			account["balance"] = balance
		}
		writeJSON(w, account)
	case "/wallet/getblock", "/wallet/getnowblock":
		writeJSON(w, map[string]any{
			"blockID": tronTestHeadBlockID,
			"block_header": map[string]any{"raw_data": map[string]any{
				"number": tronTestHeadNumber, "timestamp": tronTestHeadTimestamp,
			}},
		})
	case "/wallet/triggerconstantcontract":
		f.serveConstant(w, req)
	case "/wallet/estimateenergy":
		f.requireABIWords(req, 2)
		if f.energyRequired == 0 {
			writeJSON(w, map[string]any{"result": map[string]any{"code": "CONTRACT_VALIDATE_ERROR",
				"message": hex.EncodeToString([]byte("this node does not support estimate energy"))}})
			return
		}
		if f.revertTransfer || f.balanceOf(req["owner_address"]).Sign() == 0 {
			writeJSON(w, map[string]any{"result": map[string]any{"code": "CONTRACT_EXE_ERROR",
				"message": hex.EncodeToString([]byte("REVERT opcode executed"))}})
			return
		}
		writeJSON(w, map[string]any{"result": map[string]any{"result": true}, "energy_required": f.energyRequired})
	case "/wallet/getcontract":
		writeJSON(w, map[string]any{"origin_address": tronTestOriginHex, "contract_address": req["value"],
			"consume_user_resource_percent": f.contractUserPercent, "origin_energy_limit": f.originEnergyLimit})
	case "/wallet/getaccountresource":
		if req["address"] != tronTestOriginHex {
			f.t.Errorf("fake TRON node: getaccountresource for %v, not the deployer", req["address"])
		}
		writeJSON(w, map[string]any{"EnergyLimit": f.originEnergyLeft + tronTestOriginEnergyUsed, "EnergyUsed": tronTestOriginEnergyUsed})
	case "/wallet/getblockbynum":
		f.serveByNumber(w, req, f.blocks)
	case "/wallet/gettransactioninfobyblocknum":
		f.serveByNumber(w, req, f.infos)
	case "/wallet/gettransactioninfobyid":
		id, _ := req["value"].(string)
		info, ok := f.txInfos[id]
		if !ok {
			info = "{}"
		}
		_, _ = io.WriteString(w, info)
	default:
		f.t.Errorf("fake TRON node: unexpected call %s %s", r.URL.Path, body)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}
}

// requireABIWords fails the test unless parameter is exactly words ABI words: the
// node prepends the method id of function_selector itself, like java-tron.
func (f *fakeTronNode) requireABIWords(req map[string]any, words int) {
	parameter, _ := req["parameter"].(string)
	if decoded, err := hex.DecodeString(parameter); err != nil || len(decoded) != words*tronABIWordBytes {
		f.t.Errorf("fake TRON node: parameter %q is not %d ABI words", parameter, words)
	}
}

func (f *fakeTronNode) serveConstant(w http.ResponseWriter, req map[string]any) {
	switch req["function_selector"] {
	case tronTRC20BalanceSelector:
		f.requireABIWords(req, 1)
		word := make([]byte, tronABIWordBytes)
		f.balanceOf(req["owner_address"]).FillBytes(word)
		writeJSON(w, map[string]any{"result": map[string]any{"result": true}, "energy_used": 935,
			"constant_result": []string{hex.EncodeToString(word)}})
	case tronTRC20TransferSelector:
		f.requireABIWords(req, 2)
		if f.revertTransfer || f.balanceOf(req["owner_address"]).Sign() == 0 {
			writeJSON(w, map[string]any{"result": map[string]any{"result": true,
				"message": hex.EncodeToString([]byte("REVERT opcode executed"))}, "energy_used": 1984, "constant_result": []string{""}})
			return
		}
		writeJSON(w, map[string]any{"result": map[string]any{"result": true}, "energy_used": f.energyUsed,
			"energy_penalty": 0, "constant_result": []string{strings.Repeat("0", 64)}})
	default:
		f.t.Errorf("fake TRON node: unexpected constant call %v", req)
	}
}

func (f *fakeTronNode) balanceOf(owner any) *big.Int {
	address, _ := owner.(string)
	if balance, ok := f.tokenBalances[strings.ToLower(address)]; ok {
		return balance
	}
	return new(big.Int)
}

func (f *fakeTronNode) serveByNumber(w http.ResponseWriter, req map[string]any, store map[uint64]json.RawMessage) {
	number, _ := req["num"].(float64)
	answer, ok := store[uint64(number)]
	if !ok {
		_, _ = io.WriteString(w, "{}")
		return
	}
	_, _ = w.Write(answer)
}

func writeJSON(w http.ResponseWriter, value any) {
	_ = json.NewEncoder(w).Encode(value)
}

// newTronTestAdapter is a Nile adapter against node, with a fixed clock and no
// rate-limit sleeps.
func newTronTestAdapter(t *testing.T, node *fakeTronNode) *TronLive {
	t.Helper()
	server := httptest.NewServer(node)
	t.Cleanup(server.Close)
	adapter := NewTronLive(TronConfig{
		ChainIDStr: models.ChainTron, ChainName: "TRON Nile", NativeSymbol: models.NativeTRX,
		RPCURL: server.URL, IsTestnet: true, Confirmations: 20, Tokens: []types.Token{tronTestNileUSDT},
	})
	adapter.now = func() time.Time { return time.UnixMilli(tronTestNow) }
	adapter.pollInterval = time.Millisecond
	adapter.retry.sleep = func(context.Context, time.Duration) error { return nil }
	return adapter
}

func mustTronHex(t *testing.T, address string) string {
	t.Helper()
	value, err := addressing.TronAddressToHex(address)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func readTronFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func requireSun(t *testing.T, name string, got *big.Int, want int64) {
	t.Helper()
	if got == nil || got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("%s = %v sun, want %d", name, got, want)
	}
}

func describeQuote(q TronFeeQuote) string {
	return fmt.Sprintf("bytes=%d bw=%v act=%v energy=%d limit=%v ref=%v", q.BandwidthBytes, q.BandwidthFee, q.ActivationFee, q.Energy, q.FeeLimit, q.EnergyIsReference)
}
