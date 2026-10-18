package evm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
)

// Sepolia block 0xb62506 (hash 0x99043b36…8bc8) holds the out-of-gas transfer of
// 0.002 ETH to the watched deposit address 0xa9c37e6b…f80a (status 0x0, gasUsed
// 21000 of 21000): the scanner recorded it as a deposit before receipts were read.
const (
	sepoliaFailedTransferBlockHash = "0x99043b360c8e3f36e69d5b38969479f3f5b5144d97d05907e4bf7668db328bc8"
	succeededTransferHash          = "0x00000000000000000000000000000000000000000000000000000000000000a1"
	secondSucceededTransferHash    = "0x00000000000000000000000000000000000000000000000000000000000000a2"
)

// failedTransferNode is a node whose block 0xb62506 holds the failed Sepolia
// transfer and two transfers that succeeded.
func failedTransferNode(t *testing.T) *mocks.FakeEVMNode {
	t.Helper()
	node := mocks.NewFakeEVMNode(t)
	for hash, status := range map[string]string{
		outOfGasWithdrawalHash:      "0x0",
		succeededTransferHash:       "0x1",
		secondSucceededTransferHash: "0x1",
	} {
		node.Receipts[hash] = mocks.FakeEVMReceipt{
			Block: outOfGasWithdrawalBlk, BlockHash: sepoliaFailedTransferBlockHash, Status: status,
			Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: 0x399c6,
		}
	}
	return node
}

func TestEVM_TransactionsSucceeded_ReadsTheOneReceiptOfASingleTransfer(t *testing.T) {
	node := failedTransferNode(t)

	statuses, err := newSepoliaAdapter(node).TransactionsSucceeded(context.Background(), outOfGasWithdrawalBlk, sepoliaFailedTransferBlockHash,
		[]string{"0x" + strings.ToUpper(outOfGasWithdrawalHash[2:])})

	if err != nil {
		t.Fatal(err)
	}
	if succeeded, known := statuses[outOfGasWithdrawalHash]; !known || succeeded {
		t.Fatalf("a status 0x0 receipt must be reported as failed under its lowercase hash, got %v", statuses)
	}
	if got := len(node.CallsTo("eth_getTransactionReceipt")); got != 1 || len(node.CallsTo("eth_getBlockReceipts")) != 0 {
		t.Fatalf("one transfer reads its own receipt only: %d eth_getTransactionReceipt, %d eth_getBlockReceipts", got, len(node.CallsTo("eth_getBlockReceipts")))
	}
}

func TestEVM_TransactionsSucceeded_ReadsTheBlockReceiptsOnceForSeveralTransfers(t *testing.T) {
	node := failedTransferNode(t)

	statuses, err := newSepoliaAdapter(node).TransactionsSucceeded(context.Background(), outOfGasWithdrawalBlk, sepoliaFailedTransferBlockHash,
		[]string{outOfGasWithdrawalHash, succeededTransferHash, secondSucceededTransferHash})

	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{outOfGasWithdrawalHash: false, succeededTransferHash: true, secondSucceededTransferHash: true}
	for hash, succeeded := range want {
		if got, known := statuses[hash]; !known || got != succeeded {
			t.Fatalf("%s: succeeded=%v known=%v, want %v", hash, got, known, succeeded)
		}
	}
	if blocks, receipts := len(node.CallsTo("eth_getBlockReceipts")), len(node.CallsTo("eth_getTransactionReceipt")); blocks != 1 || receipts != 0 {
		t.Fatalf("several transfers of one block take one eth_getBlockReceipts, got %d and %d eth_getTransactionReceipt", blocks, receipts)
	}
}

func TestEVM_TransactionsSucceeded_ReadsEachReceiptWhereTheNodeHasNoBlockReceipts(t *testing.T) {
	node := failedTransferNode(t)
	node.BlockReceiptsErrorCode = -32601
	adapter := newSepoliaAdapter(node)
	hashes := []string{outOfGasWithdrawalHash, succeededTransferHash}

	for pass := 1; pass <= 2; pass++ {
		statuses, err := adapter.TransactionsSucceeded(context.Background(), outOfGasWithdrawalBlk, sepoliaFailedTransferBlockHash, hashes)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if statuses[outOfGasWithdrawalHash] || !statuses[succeededTransferHash] {
			t.Fatalf("pass %d: statuses %v", pass, statuses)
		}
	}
	if blocks, receipts := len(node.CallsTo("eth_getBlockReceipts")), len(node.CallsTo("eth_getTransactionReceipt")); blocks != 1 || receipts != 4 {
		t.Fatalf("a node without eth_getBlockReceipts is asked once, then each receipt is read: %d eth_getBlockReceipts, %d eth_getTransactionReceipt", blocks, receipts)
	}
}

func TestEVM_TransactionsSucceeded_ANodeThatRefusesBlockReceiptsIsAskedAgainNextTime(t *testing.T) {
	node := failedTransferNode(t)
	node.BlockReceiptsErrorCode = -32000
	adapter := newSepoliaAdapter(node)
	hashes := []string{outOfGasWithdrawalHash, succeededTransferHash}

	for pass := 1; pass <= 2; pass++ {
		statuses, err := adapter.TransactionsSucceeded(context.Background(), outOfGasWithdrawalBlk, sepoliaFailedTransferBlockHash, hashes)
		if err != nil || statuses[outOfGasWithdrawalHash] || !statuses[succeededTransferHash] {
			t.Fatalf("pass %d: a refused eth_getBlockReceipts falls back to each receipt, got %v err %v", pass, statuses, err)
		}
	}
	if blocks := len(node.CallsTo("eth_getBlockReceipts")); blocks != 2 {
		t.Fatalf("a rejection other than method not found is not remembered: %d eth_getBlockReceipts in two reads", blocks)
	}
}

func TestEVM_TransactionsSucceeded_AReceiptThatCannotBeReadIsAnError(t *testing.T) {
	const unknownHash = "0x00000000000000000000000000000000000000000000000000000000000000ff"
	for _, tc := range []struct {
		name   string
		hashes []string
		node   func(node *mocks.FakeEVMNode)
	}{
		{"no receipt for the transaction", []string{unknownHash}, func(*mocks.FakeEVMNode) {}},
		{"no receipt among the block receipts", []string{succeededTransferHash, unknownHash}, func(*mocks.FakeEVMNode) {}},
		{"receipt of another block", []string{succeededTransferHash}, func(node *mocks.FakeEVMNode) {
			reorged := node.Receipts[succeededTransferHash]
			reorged.BlockHash = mocks.FakeEVMBlockHash(outOfGasWithdrawalBlk)
			node.Receipts[succeededTransferHash] = reorged
		}},
		{"block receipts of another block", []string{succeededTransferHash, secondSucceededTransferHash}, func(node *mocks.FakeEVMNode) {
			reorged := node.Receipts[secondSucceededTransferHash]
			reorged.BlockHash = mocks.FakeEVMBlockHash(outOfGasWithdrawalBlk)
			node.Receipts[secondSucceededTransferHash] = reorged
		}},
		{"receipt without a status", []string{succeededTransferHash}, func(node *mocks.FakeEVMNode) {
			pre := node.Receipts[succeededTransferHash]
			pre.Status = ""
			node.Receipts[succeededTransferHash] = pre
		}},
		{"block receipts the node does not have", []string{succeededTransferHash, secondSucceededTransferHash}, func(node *mocks.FakeEVMNode) {
			node.Receipts = map[string]mocks.FakeEVMReceipt{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := failedTransferNode(t)
			tc.node(node)

			statuses, err := newSepoliaAdapter(node).TransactionsSucceeded(context.Background(), outOfGasWithdrawalBlk, sepoliaFailedTransferBlockHash, tc.hashes)

			if err == nil || statuses != nil {
				t.Fatalf("an unread receipt must fail the whole read, got statuses %v err %v", statuses, err)
			}
		})
	}
}

func TestEVM_TransactionsSucceeded_AnOutageIsAProviderFailureAndNotAFallback(t *testing.T) {
	for _, hashes := range [][]string{{succeededTransferHash}, {succeededTransferHash, secondSucceededTransferHash}} {
		node := failedTransferNode(t)
		node.ReceiptsHTTPStatus = http.StatusServiceUnavailable

		statuses, err := newSepoliaAdapter(node).TransactionsSucceeded(context.Background(), outOfGasWithdrawalBlk, sepoliaFailedTransferBlockHash, hashes)

		if statuses != nil || !errors.Is(err, chain.ErrProviderUnavailable) {
			t.Fatalf("%d transfers: an outage must be chain.ErrProviderUnavailable, got statuses %v err %v", len(hashes), statuses, err)
		}
		if len(hashes) > 1 && len(node.CallsTo("eth_getTransactionReceipt")) != 0 {
			t.Fatal("an outage of eth_getBlockReceipts is not a node without the method: nothing falls back")
		}
	}
}

func TestEVM_TransactionsSucceeded_NoTransfersReadNothing(t *testing.T) {
	node := failedTransferNode(t)

	statuses, err := newSepoliaAdapter(node).TransactionsSucceeded(context.Background(), outOfGasWithdrawalBlk, sepoliaFailedTransferBlockHash, nil)

	if err != nil || len(statuses) != 0 || len(node.Calls()) != 0 {
		t.Fatalf("statuses %v err %v calls %d", statuses, err, len(node.Calls()))
	}
}
