package evm

import (
	"context"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
)

// The Sepolia withdrawal built with the fixed 21000 limit: mined in block
// 0xb62506 with status 0x0, gasUsed 21000 of 21000. Its value was not moved.
const (
	outOfGasWithdrawalHash = "0x235681c8abaf2b4b72f02fb9e32b2035c9c27b1afec6933379be2bce07dd26cb"
	outOfGasWithdrawalBlk  = uint64(0xb62506)
)

func TestEVM_GetTransactionBlock_FailsAMinedTransactionWhoseReceiptFailed(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	node.Receipts[outOfGasWithdrawalHash] = mocks.FakeEVMReceipt{
		Block: outOfGasWithdrawalBlk, Status: "0x0", Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: 0x399c6,
	}
	const revertedHash = "0x00000000000000000000000000000000000000000000000000000000000000aa"
	node.Receipts[revertedHash] = mocks.FakeEVMReceipt{Block: 120, Status: "0x0", Gas: 100_000, GasUsed: 31_000, EffectiveGasPrice: 1}
	adapter := newSepoliaAdapter(node)

	for _, tc := range []struct {
		name   string
		hash   string
		result string
		block  uint64
	}{
		{"out of gas", outOfGasWithdrawalHash, "out_of_gas", outOfGasWithdrawalBlk},
		{"reverted", revertedHash, "reverted", 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block, err := adapter.GetTransactionBlock(context.Background(), tc.hash)
			var failed *chain.TransactionFailedError
			if !errors.As(err, &failed) || !errors.Is(err, chain.ErrTransactionFailed) {
				t.Fatalf("a status 0x0 receipt must be chain.TransactionFailedError, got block %d err %v", block, err)
			}
			if failed.Result != tc.result || failed.Block != tc.block || block != 0 {
				t.Fatalf("got result %q block %d (returned %d), want %q in block %d", failed.Result, failed.Block, block, tc.result, tc.block)
			}
		})
	}
}

func TestEVM_GetTransactionBlock_ReturnsTheBlockOfASuccessfulReceipt(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	const hash = "0x00000000000000000000000000000000000000000000000000000000000000bb"
	node.Receipts[hash] = mocks.FakeEVMReceipt{Block: 4242, Status: "0x1", Gas: 25_200, GasUsed: 21_000, EffectiveGasPrice: 1}

	block, err := newSepoliaAdapter(node).GetTransactionBlock(context.Background(), hash)
	if err != nil || block != 4242 {
		t.Fatalf("block %d err %v, want 4242", block, err)
	}
}

func TestEVM_GetTransactionBlock_PendingAndUnknownTransactionsAreNotInABlock(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	const pending = "0x00000000000000000000000000000000000000000000000000000000000000cc"
	node.Receipts[pending] = mocks.FakeEVMReceipt{Gas: 25_200, EffectiveGasPrice: 1}
	adapter := newSepoliaAdapter(node)

	for _, hash := range []string{pending, "0x00000000000000000000000000000000000000000000000000000000000000dd"} {
		block, err := adapter.GetTransactionBlock(context.Background(), hash)
		if err != nil || block != 0 {
			t.Fatalf("%s: block %d err %v, want 0 and no error", hash, block, err)
		}
	}
}

func TestEVM_GetTransactionBlock_AReceiptWithoutStatusIsNotConfirmed(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	const hash = "0x00000000000000000000000000000000000000000000000000000000000000ee"
	node.Receipts[hash] = mocks.FakeEVMReceipt{Block: 50, Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: 1}

	block, err := newSepoliaAdapter(node).GetTransactionBlock(context.Background(), hash)
	if err == nil || block != 0 || errors.Is(err, chain.ErrTransactionFailed) {
		t.Fatalf("a receipt without a status is neither confirmed nor failed, got block %d err %v", block, err)
	}
}
