package evm

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/macrowallets/waas/app/services/chain"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	// 0x2540be400 = 10_000_000_000 wei quoted by the GasPriceOracle.
	feeTestL1FeeHex = "0x2540be400"
	feeTestL1Fee    = int64(10_000_000_000)
	// 0x54e5 = 21_733 gas, Arbitrum Sepolia's estimate for a zero-value transfer.
	feeTestArbitrumNativeEstimateHex = "0x54e5"
	feeTestArbitrumNativeEstimate    = uint64(21_733)
	feeTestGasPrice                  = int64(1_000_000_000)
)

var feeTestBaseUSDC = types.Token{Symbol: "USDC", Contract: models.USDCContractBaseSepolia, Decimals: 6, ChainID: models.ChainBase}

func newNetworkAdapter(node *mocks.FakeEVMNode, chainID, native string, networkID int64) *EVMLive {
	return NewEVMLive(EVMConfig{
		ChainIDStr: chainID, NativeSymbol: native, NativeDecimal: 18, NetworkID: networkID, RPCURL: node.URL(),
	})
}

func newBaseSepoliaAdapter(t *testing.T) (*EVMLive, *mocks.FakeEVMNode) {
	t.Helper()
	node := mocks.NewFakeEVMNode(t)
	node.L1FeeHex = feeTestL1FeeHex
	return newNetworkAdapter(node, models.ChainBase, models.NativeETH, models.EVMNetworkIDBaseSepolia), node
}

func bufferedL2Fee(gasLimit uint64) *big.Int {
	return new(big.Int).Mul(big.NewInt(evmGasPriceMultiplier*feeTestGasPrice), new(big.Int).SetUint64(gasLimit))
}

func bufferedL1Fee() *big.Int {
	return big.NewInt(feeTestL1Fee * evmL1DataFeeMultiplier)
}

func TestBase_Native_TransferReserveAddsTheBufferedL1DataFee(t *testing.T) {
	adapter, node := newBaseSepoliaAdapter(t)

	fee, minimumRemaining, err := adapter.NativeTransferReserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Add(bufferedL2Fee(evmNativeTransferGasLimit), bufferedL1Fee())
	if fee.Cmp(want) != 0 {
		t.Fatalf("reserve %s wei, want 21000 × 2 gwei + 2 × L1 fee = %s wei", fee, want)
	}
	if minimumRemaining.Sign() != 0 {
		t.Fatalf("EVM keeps no minimum balance, got %s", minimumRemaining)
	}
	calls := node.CallsToContract(mocks.FakeEVMGasPriceOracle)
	if len(calls) != 1 {
		t.Fatalf("expected one GasPriceOracle quote, got %d", len(calls))
	}
	var call struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(calls[0].Params[0], &call); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(call.Data, "0x"+opStackGetL1FeeSelector) {
		t.Fatalf("the oracle must be asked getL1Fee(bytes), got %s", call.Data)
	}
}

func TestBase_Native_SweepLeavesRoomForTheL1DataFee(t *testing.T) {
	adapter, _ := newBaseSepoliaAdapter(t)
	balance := big.NewInt(1_000_000_000_000_000)

	txs, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From: gasTestFrom, To: gasTestTo, NativeBalance: balance,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("a native sweep is one transfer, got %d", len(txs))
	}
	value, _ := new(big.Int).SetString(txs[0].Metadata["value"].(string), 10)
	l2Fee := new(big.Int).Mul(builtGasPrice(t, &txs[0]), new(big.Int).SetUint64(builtGasLimit(t, &txs[0])))
	spent := new(big.Int).Add(value, l2Fee)
	spent.Add(spent, bufferedL1Fee())
	if spent.Cmp(balance) != 0 {
		t.Fatalf("value %s + gas %s + L1 fee %s = %s, want the whole balance %s", value, l2Fee, bufferedL1Fee(), spent, balance)
	}
}

func TestBase_Token_SweepSeedsGasForTheL1DataFee(t *testing.T) {
	adapter, node := newBaseSepoliaAdapter(t)
	node.EstimateGasHex = "0xc350" // 50_000 × 125% = 62_500 → floored to 65_000
	node.TokenBalanceHex = "0x" + big.NewInt(5_000_000).Text(16)

	txs, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From: gasTestFrom, To: gasTestTo, Token: &feeTestBaseUSDC, NativeBalance: new(big.Int),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("an unfunded child needs gas_seed + sweep, got %d txs", len(txs))
	}
	seed, _ := new(big.Int).SetString(txs[0].Metadata["value"].(string), 10)
	needed := new(big.Int).Add(bufferedL2Fee(evmERC20TransferGasFloor), bufferedL1Fee())
	want := new(big.Int).Mul(needed, big.NewInt(evmGasSeedBufferPercent))
	want.Div(want, big.NewInt(percentDenominator))
	if seed.Cmp(want) != 0 {
		t.Fatalf("gas_seed %s wei, want 120%% of (gas %s + L1 fee %s) = %s", seed, bufferedL2Fee(evmERC20TransferGasFloor), bufferedL1Fee(), want)
	}
}

func TestBase_Estimate_FeeIncludesTheL1DataFee(t *testing.T) {
	adapter, _ := newBaseSepoliaAdapter(t)

	estimate, err := adapter.EstimateFee(context.Background(), types.TransferRequest{
		From: gasTestFrom, To: gasTestTo, Amount: big.NewInt(1), Asset: models.NativeETH,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Add(bufferedL2Fee(evmNativeTransferGasLimit), bufferedL1Fee())
	if estimate.Fee != fmtUnits(want, 18) {
		t.Fatalf("fee %s ETH, want %s", estimate.Fee, fmtUnits(want, 18))
	}
}

func TestBase_L1_DataFeeFailureStopsTheReserve(t *testing.T) {
	node := mocks.NewFakeEVMNode(t) // no L1FeeHex: the oracle call reverts
	adapter := newNetworkAdapter(node, models.ChainBase, models.NativeETH, models.EVMNetworkIDBaseSepolia)

	if _, _, err := adapter.NativeTransferReserve(context.Background()); !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("an unquotable L1 fee must fail the reserve, got %v", err)
	}
	zero := mocks.NewFakeEVMNode(t)
	zero.L1FeeHex = "0x0"
	adapter = newNetworkAdapter(zero, models.ChainBase, models.NativeETH, models.EVMNetworkIDBaseMainnet)
	if _, _, err := adapter.NativeTransferReserve(context.Background()); !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("a zero L1 fee quote must not reserve nothing, got %v", err)
	}
}

func TestArbitrum_Native_TransfersUseThePaddedNodeEstimate(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = feeTestArbitrumNativeEstimateHex
	adapter := newNetworkAdapter(node, models.ChainArbitrum, models.NativeETH, models.EVMNetworkIDArbitrumSepolia)
	wantLimit := feeTestArbitrumNativeEstimate * evmArbitrumNativeGasMarginPercent / percentDenominator

	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: gasTestFrom, To: gasTestTo, Amount: big.NewInt(1), Asset: models.NativeETH,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := builtGasLimit(t, unsigned); got != wantLimit {
		t.Fatalf("native gas limit %d, want %d (estimate × %d%%); 21000 is rejected on Arbitrum", got, wantLimit, evmArbitrumNativeGasMarginPercent)
	}
	fee, _, err := adapter.NativeTransferReserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := bufferedL2Fee(wantLimit); fee.Cmp(want) != 0 {
		t.Fatalf("reserve %s, want %s", fee, want)
	}
	if calls := node.CallsToContract(mocks.FakeEVMGasPriceOracle); len(calls) != 0 {
		t.Fatalf("Arbitrum has no GasPriceOracle, got %d calls", len(calls))
	}
	var estimate struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(node.CallsTo("eth_estimateGas")[0].Params[0], &estimate); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(estimate.From, gasTestFrom) || !strings.EqualFold(estimate.To, gasTestTo) || estimate.Value != "0x0" {
		t.Fatalf("the native estimate must be a zero-value transfer between the real addresses, got %+v", estimate)
	}
}

func TestArbitrum_Native_SweepEncodesTheLimitItReserved(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = feeTestArbitrumNativeEstimateHex
	adapter := newNetworkAdapter(node, models.ChainArbitrum, models.NativeETH, models.EVMNetworkIDArbitrumMainnet)
	balance := big.NewInt(1_000_000_000_000_000)

	txs, err := adapter.BuildSweep(context.Background(), types.SweepRequest{From: gasTestFrom, To: gasTestTo, NativeBalance: balance})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := new(big.Int).SetString(txs[0].Metadata["value"].(string), 10)
	fee := new(big.Int).Mul(builtGasPrice(t, &txs[0]), new(big.Int).SetUint64(builtGasLimit(t, &txs[0])))
	if new(big.Int).Add(value, fee).Cmp(balance) != 0 {
		t.Fatalf("value %s + gas %s must equal the balance %s", value, fee, balance)
	}
	if builtGasLimit(t, &txs[0]) <= evmNativeTransferGasLimit {
		t.Fatalf("Arbitrum sweep must not encode the 21000 limit")
	}
}

func TestNative_Sweep_ReportsTheValueItEncodes(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = feeTestArbitrumNativeEstimateHex
	adapter := newNetworkAdapter(node, models.ChainArbitrum, models.NativeETH, models.EVMNetworkIDArbitrumSepolia)
	plannedAmount := big.NewInt(1)

	txs, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From: gasTestFrom, To: gasTestTo, Amount: plannedAmount, NativeBalance: big.NewInt(1_000_000_000_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := new(big.Int).SetString(txs[0].Metadata["value"].(string), 10)
	if txs[0].TransferAmount == nil || txs[0].TransferAmount.Cmp(value) != 0 {
		t.Fatalf("transfer amount %v, want the encoded value %s (not the planned %s)", txs[0].TransferAmount, value, plannedAmount)
	}
}

func TestToken_Sweep_ReportsTheSeedAndTokenAmounts(t *testing.T) {
	adapter, node := newBaseSepoliaAdapter(t)
	node.EstimateGasHex = "0xc350"
	tokenAmount := big.NewInt(5_000_000)
	node.TokenBalanceHex = "0x" + tokenAmount.Text(16)

	txs, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From: gasTestFrom, To: gasTestTo, Token: &feeTestBaseUSDC, NativeBalance: new(big.Int),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("expected gas_seed + sweep, got %d txs", len(txs))
	}
	seedValue, _ := new(big.Int).SetString(txs[0].Metadata["value"].(string), 10)
	if txs[0].TransferAmount == nil || txs[0].TransferAmount.Cmp(seedValue) != 0 {
		t.Fatalf("gas_seed transfer amount %v, want its native value %s", txs[0].TransferAmount, seedValue)
	}
	if txs[1].TransferAmount == nil || txs[1].TransferAmount.Cmp(tokenAmount) != 0 {
		t.Fatalf("token sweep transfer amount %v, want the token amount %s", txs[1].TransferAmount, tokenAmount)
	}
}

func TestArbitrum_Native_EstimateFailureFailsTheTransfer(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasError = "rate limited"
	adapter := newNetworkAdapter(node, models.ChainArbitrum, models.NativeETH, models.EVMNetworkIDArbitrumSepolia)

	if _, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: gasTestFrom, To: gasTestTo, Amount: big.NewInt(1), Asset: models.NativeETH,
	}); !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("expected chain.ErrGasEstimateFailed, got %v", err)
	}
}

func TestStandard_Networks_KeepTheFixedNativeLimitAndNoL1Fee(t *testing.T) {
	for _, networkID := range []int64{
		models.EVMNetworkIDEthereumMainnet, models.EVMNetworkIDEthereumSepolia,
		models.EVMNetworkIDPolygonMainnet, models.EVMNetworkIDPolygonAmoy,
		models.EVMNetworkIDBSCMainnet, models.EVMNetworkIDBSCTestnet,
	} {
		node := mocks.NewFakeEVMNode(t)
		adapter := newNetworkAdapter(node, "evm", "eth", networkID)

		fee, _, err := adapter.NativeTransferReserve(context.Background())
		if err != nil {
			t.Fatalf("%d: %v", networkID, err)
		}
		if want := bufferedL2Fee(evmNativeTransferGasLimit); fee.Cmp(want) != 0 {
			t.Fatalf("%d: reserve %s, want %s", networkID, fee, want)
		}
		l1Fee, err := adapter.EstimateL1DataFee(context.Background(), types.TransferRequest{From: gasTestFrom, To: gasTestTo})
		if err != nil || l1Fee.Sign() != 0 {
			t.Fatalf("%d: L1 fee %v err %v, want 0", networkID, l1Fee, err)
		}
		if n := len(node.CallsTo("eth_estimateGas")) + len(node.CallsTo("eth_call")); n != 0 {
			t.Fatalf("%d: standard networks need no estimate or oracle call, got %d", networkID, n)
		}
	}
}

func TestBSC_Signs_LegacyTransactionsForItsChainID(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	adapter := newNetworkAdapter(node, models.ChainBSC, models.NativeBNB, models.EVMNetworkIDBSCTestnet)

	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: gasTestFrom, To: gasTestTo, Amount: big.NewInt(1), Asset: models.NativeBNB,
	})
	if err != nil {
		t.Fatal(err)
	}
	transaction, signer, err := adapter.transactionFromUnsigned(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if transaction.Type() != 0 || signer.ChainID().Int64() != models.EVMNetworkIDBSCTestnet {
		t.Fatalf("want a legacy EIP-155 tx for chain 97, got type %d chain %s", transaction.Type(), signer.ChainID())
	}
	if builtGasLimit(t, unsigned) != evmNativeTransferGasLimit {
		t.Fatalf("BSC native transfers keep the 21000 limit")
	}
}

func TestAbi_Encode_BytesPadsToWholeWords(t *testing.T) {
	encoded := abiEncodeBytes([]byte{0xde, 0xad})
	want := strings.Repeat("0", 62) + "20" + strings.Repeat("0", 63) + "2" + "dead" + strings.Repeat("0", 60)
	if encoded != want {
		t.Fatalf("got %s\nwant %s", encoded, want)
	}
	if empty := abiEncodeBytes(nil); len(empty) != 2*2*abiWordBytes {
		t.Fatalf("empty bytes encode to offset + length only, got %d hex chars", len(empty))
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		t.Fatal(err)
	}
}

// logScanNode answers one block with a native deposit and fails eth_getLogs.
func logScanNode(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var req struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		body := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "eth_getBlockByNumber":
			body["result"] = map[string]interface{}{
				"hash": "0xb1", "timestamp": "0x1",
				"transactions": []map[string]string{{"hash": "0xt1", "from": gasTestFrom, "to": gasTestTo, "value": "0x10"}},
			}
		default:
			body["error"] = map[string]interface{}{"code": -32005, "message": "rate limited"}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestStrict_Log_ScanFailsTheBlockWhenGetLogsFails(t *testing.T) {
	server := logScanNode(t)
	strict := NewEVMLive(EVMConfig{ChainIDStr: models.ChainBase, NativeSymbol: "eth", NetworkID: models.EVMNetworkIDBaseSepolia, RPCURL: server.URL, StrictLogScan: true})
	if _, err := strict.ScanBlock(context.Background(), 1); err == nil {
		t.Fatal("a strict scan must fail the block so the scanner retries it")
	}

	lenient := NewEVMLive(EVMConfig{ChainIDStr: models.ChainETH, NativeSymbol: "eth", NetworkID: models.EVMNetworkIDEthereumSepolia, RPCURL: server.URL})
	transfers, err := lenient.ScanBlock(context.Background(), 1)
	if err != nil || len(transfers) != 1 {
		t.Fatalf("the deployed chains keep native-only results, got %d transfers err %v", len(transfers), err)
	}
}
