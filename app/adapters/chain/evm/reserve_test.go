package evm

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestEVMNativeTransferReserveIsTheBuiltNativeTransferFee(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	adapter := newGasTestAdapter(t, node)

	fee, minimumRemaining, err := adapter.NativeTransferReserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: gasTestFrom, To: gasTestTo, Amount: big.NewInt(1), Asset: "matic",
	})
	if err != nil {
		t.Fatal(err)
	}
	built := new(big.Int).Mul(builtGasPrice(t, unsigned), new(big.Int).SetUint64(builtGasLimit(t, unsigned)))
	if fee.Cmp(built) != 0 {
		t.Fatalf("reserve %s wei, but the built native transfer can spend %s wei", fee, built)
	}
	// 1 gwei suggested × 2 buffer × 21000.
	if want := big.NewInt(2 * 1_000_000_000 * 21_000); fee.Cmp(want) != 0 {
		t.Fatalf("fee %s, want %s", fee, want)
	}
	if minimumRemaining == nil || minimumRemaining.Sign() != 0 {
		t.Fatalf("EVM keeps no minimum balance, got %v", minimumRemaining)
	}
}

func TestEVMNativeTransferReserveFailsWithoutAUsableGasPrice(t *testing.T) {
	zero := mocks.NewFakeEVMNode(t)
	zero.GasPriceHex = "0x0"
	if _, _, err := newGasTestAdapter(t, zero).NativeTransferReserve(context.Background()); err == nil {
		t.Fatal("a zero gas price must not reserve zero gas")
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer down.Close()
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "polygon", NativeSymbol: "matic", RPCURL: down.URL})
	if _, _, err := adapter.NativeTransferReserve(context.Background()); err == nil {
		t.Fatal("a failed eth_gasPrice must fail the reserve")
	}
}
