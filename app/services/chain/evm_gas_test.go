package chain

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	gasTestFrom     = "0xdea40439a0736b4ec13dd28556e01465b8ceb39f"
	gasTestTo       = "0x53bc147071251db8294b55a303a4570dab595178"
	gasTestContract = "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	gasTestNetwork  = int64(80002)
)

var gasTestToken = types.Token{Symbol: "USDC", Contract: gasTestContract, Decimals: 6, ChainID: "polygon"}

func newGasTestAdapter(t *testing.T, node *mocks.FakeEVMNode) *EVMLive {
	t.Helper()
	return NewEVMLive(EVMConfig{
		ChainIDStr:    "polygon",
		NativeSymbol:  "matic",
		NativeDecimal: 18,
		NetworkID:     gasTestNetwork,
		RPCURL:        node.URL(),
	})
}

func tokenTransferRequest() types.TransferRequest {
	return types.TransferRequest{
		From:   gasTestFrom,
		To:     gasTestTo,
		Amount: big.NewInt(3_000_000),
		Asset:  gasTestToken.Symbol,
		Token:  &gasTestToken,
	}
}

func builtGasLimit(t *testing.T, unsigned *types.UnsignedTx) uint64 {
	t.Helper()
	limit, ok := unsigned.Metadata["gas_limit"].(uint64)
	if !ok {
		t.Fatalf("gas_limit metadata missing or not uint64: %#v", unsigned.Metadata["gas_limit"])
	}
	return limit
}

func builtGasPrice(t *testing.T, unsigned *types.UnsignedTx) *big.Int {
	t.Helper()
	raw, ok := unsigned.Metadata["gas_price"].(string)
	if !ok {
		t.Fatalf("gas_price metadata missing: %#v", unsigned.Metadata["gas_price"])
	}
	price, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		t.Fatalf("gas_price metadata is not decimal: %q", raw)
	}
	return price
}

func TestEVMBuildTransfer_TokenUsesEstimateWithMargin(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = "0x134a4" // 79_012
	adapter := newGasTestAdapter(t, node)

	unsigned, err := adapter.BuildTransfer(context.Background(), tokenTransferRequest())
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}

	if got, want := builtGasLimit(t, unsigned), uint64(98_765); got != want {
		t.Fatalf("gas_limit = %d, want 79_012 × 125%% = %d", got, want)
	}

	estimates := node.CallsTo("eth_estimateGas")
	if len(estimates) != 1 {
		t.Fatalf("expected one eth_estimateGas call, got %d", len(estimates))
	}
	var call struct {
		From string `json:"from"`
		To   string `json:"to"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(estimates[0].Params[0], &call); err != nil {
		t.Fatalf("decode eth_estimateGas params: %v", err)
	}
	wantData := "0x" + hex.EncodeToString(encodeERC20Transfer(gasTestTo, big.NewInt(3_000_000)))
	if !strings.EqualFold(call.From, gasTestFrom) || !strings.EqualFold(call.To, gasTestContract) || call.Data != wantData {
		t.Fatalf("eth_estimateGas must simulate the real transfer; got from=%s to=%s data=%s", call.From, call.To, call.Data)
	}
}

func TestEVMBuildTransfer_TokenEstimateBelowFloorUsesFloor(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = "0x9c40" // 40_000 → 50_000 with margin, below the floor
	adapter := newGasTestAdapter(t, node)

	unsigned, err := adapter.BuildTransfer(context.Background(), tokenTransferRequest())
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}
	if got := builtGasLimit(t, unsigned); got != evmERC20TransferGasFloor {
		t.Fatalf("gas_limit = %d, want floor %d", got, evmERC20TransferGasFloor)
	}
}

func TestEVMBuildTransfer_TokenEstimateErrorFailsWithoutFallback(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasError = "execution reverted"
	adapter := newGasTestAdapter(t, node)

	unsigned, err := adapter.BuildTransfer(context.Background(), tokenTransferRequest())
	if err == nil {
		t.Fatalf("expected an error, got unsigned tx with gas_limit %v", unsigned.Metadata["gas_limit"])
	}
	if !errors.Is(err, ErrGasEstimateFailed) {
		t.Fatalf("expected ErrGasEstimateFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "execution reverted") || !strings.Contains(err.Error(), "USDC") {
		t.Fatalf("error should name the token and the node reason, got %q", err.Error())
	}
	if sent := node.CallsTo("eth_sendRawTransaction"); len(sent) != 0 {
		t.Fatalf("nothing may be broadcast after a failed estimate, got %d sends", len(sent))
	}
}

func TestEVMBuildTransfer_TokenZeroEstimateIsAnError(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = "0x0"
	adapter := newGasTestAdapter(t, node)

	if _, err := adapter.BuildTransfer(context.Background(), tokenTransferRequest()); !errors.Is(err, ErrGasEstimateFailed) {
		t.Fatalf("expected ErrGasEstimateFailed for a zero estimate, got %v", err)
	}
}

func TestEVMBuildTransfer_NativeKeepsFixedLimitWithoutEstimate(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	adapter := newGasTestAdapter(t, node)

	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: gasTestFrom, To: gasTestTo, Amount: big.NewInt(1), Asset: "matic",
	})
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}
	if got := builtGasLimit(t, unsigned); got != evmNativeTransferGasLimit {
		t.Fatalf("native gas_limit = %d, want %d", got, evmNativeTransferGasLimit)
	}
	if calls := node.CallsTo("eth_estimateGas"); len(calls) != 0 {
		t.Fatalf("native transfers must not call eth_estimateGas, got %d", len(calls))
	}
}

func TestEVMBuildTransfer_CallerGasLimitSkipsEstimate(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	adapter := newGasTestAdapter(t, node)
	req := tokenTransferRequest()
	presized := uint64(90_000)
	req.GasLimit = &presized

	unsigned, err := adapter.BuildTransfer(context.Background(), req)
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}
	if got := builtGasLimit(t, unsigned); got != presized {
		t.Fatalf("gas_limit = %d, want caller-supplied %d", got, presized)
	}
	if calls := node.CallsTo("eth_estimateGas"); len(calls) != 0 {
		t.Fatalf("a caller-supplied limit must not be re-estimated, got %d calls", len(calls))
	}
}

func TestEVMEstimateFee_TokenMatchesBuiltGasTimesPrice(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = "0x134a4"
	adapter := newGasTestAdapter(t, node)

	fee, err := adapter.EstimateFee(context.Background(), tokenTransferRequest())
	if err != nil {
		t.Fatalf("EstimateFee: %v", err)
	}
	unsigned, err := adapter.BuildTransfer(context.Background(), tokenTransferRequest())
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}

	limit := builtGasLimit(t, unsigned)
	price := builtGasPrice(t, unsigned)
	if fee.GasLimit != limit || fee.GasPrice != price.String() {
		t.Fatalf("fee estimate (limit %d, price %s) must match the built tx (limit %d, price %s)",
			fee.GasLimit, fee.GasPrice, limit, price)
	}
	wantFee := fmtUnits(new(big.Int).Mul(price, new(big.Int).SetUint64(limit)), 18)
	if fee.Fee != wantFee {
		t.Fatalf("fee = %s, want gas × price = %s", fee.Fee, wantFee)
	}
}

func TestEVMEstimateFee_TokenEstimateErrorIsReturned(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasError = "execution reverted"
	adapter := newGasTestAdapter(t, node)

	if _, err := adapter.EstimateFee(context.Background(), tokenTransferRequest()); !errors.Is(err, ErrGasEstimateFailed) {
		t.Fatalf("expected ErrGasEstimateFailed, got %v", err)
	}
}

func TestEVMBuildSweep_TokenSeedsAndSweepsWithEstimatedLimit(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = "0x134a4"
	adapter := newGasTestAdapter(t, node)

	unsigneds, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From:          gasTestTo,
		To:            gasTestFrom,
		Asset:         gasTestToken.Symbol,
		Amount:        big.NewInt(3_000_000),
		NativeBalance: big.NewInt(0),
		Token:         &gasTestToken,
	})
	if err != nil {
		t.Fatalf("BuildSweep: %v", err)
	}
	if len(unsigneds) != 2 {
		t.Fatalf("expected gas_seed + sweep, got %d txs", len(unsigneds))
	}
	seed, sweep := unsigneds[0], unsigneds[1]

	sweepLimit := builtGasLimit(t, &sweep)
	if sweepLimit != 98_765 {
		t.Fatalf("sweep gas_limit = %d, want estimated 98_765", sweepLimit)
	}
	price := builtGasPrice(t, &sweep)
	seedValue, ok := new(big.Int).SetString(seed.Metadata["value"].(string), 10)
	if !ok {
		t.Fatalf("seed value is not decimal: %v", seed.Metadata["value"])
	}
	sweepFee := new(big.Int).Mul(price, new(big.Int).SetUint64(sweepLimit))
	if seedValue.Cmp(sweepFee) < 0 {
		t.Fatalf("gas_seed %s does not cover the sweep fee %s", seedValue, sweepFee)
	}
}

func TestEVMBuildSweep_TokenSkipsSeedWhenNativeCoversEstimatedFee(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = "0x134a4"
	adapter := newGasTestAdapter(t, node)
	buffered := bufferedEVMGasPrice(hexToBigInt(node.GasPriceHex))
	enough := new(big.Int).Mul(buffered, big.NewInt(98_765))
	justShort := new(big.Int).Sub(enough, big.NewInt(1))

	for _, tc := range []struct {
		name    string
		balance *big.Int
		txs     int
	}{
		{name: "covers estimated fee", balance: enough, txs: 1},
		{name: "one wei short of estimated fee", balance: justShort, txs: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsigneds, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
				From: gasTestTo, To: gasTestFrom, Asset: gasTestToken.Symbol,
				Amount: big.NewInt(3_000_000), NativeBalance: tc.balance, Token: &gasTestToken,
			})
			if err != nil {
				t.Fatalf("BuildSweep: %v", err)
			}
			if len(unsigneds) != tc.txs {
				t.Fatalf("expected %d txs, got %d", tc.txs, len(unsigneds))
			}
		})
	}
}

func TestEVMEstimateTransferGasLimit_RejectsIncompleteTokenRequest(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	adapter := newGasTestAdapter(t, node)

	for name, req := range map[string]types.TransferRequest{
		"missing from":   {To: gasTestTo, Amount: big.NewInt(1), Token: &gasTestToken},
		"missing to":     {From: gasTestFrom, Amount: big.NewInt(1), Token: &gasTestToken},
		"missing amount": {From: gasTestFrom, To: gasTestTo, Token: &gasTestToken},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := adapter.EstimateTransferGasLimit(context.Background(), req); !errors.Is(err, ErrGasEstimateFailed) {
				t.Fatalf("expected ErrGasEstimateFailed, got %v", err)
			}
		})
	}
	if calls := node.CallsTo("eth_estimateGas"); len(calls) != 0 {
		t.Fatalf("incomplete requests must fail before calling the node, got %d calls", len(calls))
	}
}
