//go:build evm_receipts_live

package evm

// Read-only check against a public Sepolia node (go test -tags evm_receipts_live
// -run TestLive_ ./app/adapters/chain/evm/): block 0xb62506 lists the 0.002 ETH
// transfer 0x235681c8…26cb to the deposit address 0xa9c37e6b…f80a with its value,
// and its receipt failed (out of gas), so it is not a deposit. Nothing is signed
// or broadcast.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

const liveSepoliaRPC = "https://ethereum-sepolia-rpc.publicnode.com"

func TestLive_SepoliaFailedTransferIsListedWithItsValueAndItsReceiptFailed(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainTETH, NativeSymbol: models.NativeETH, NativeDecimal: 18, RPCURL: liveSepoliaRPC})
	ctx := context.Background()

	transfers, err := adapter.ScanBlock(ctx, outOfGasWithdrawalBlk)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	listed := false
	for _, transfer := range transfers {
		if transfer.Token != nil {
			if strings.EqualFold(transfer.TxHash, outOfGasWithdrawalHash) {
				t.Fatal("a failed transaction emitted a Transfer log")
			}
			continue
		}
		hashes = append(hashes, transfer.TxHash)
		if strings.EqualFold(transfer.TxHash, outOfGasWithdrawalHash) {
			listed = transfer.Amount.String() == "2000000000000000" &&
				strings.EqualFold(transfer.To, "0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a") &&
				strings.EqualFold(transfer.BlockHash, sepoliaFailedTransferBlockHash)
		}
	}
	if !listed {
		t.Fatal("the block no longer lists the failed transfer with its value")
	}

	single, err := adapter.TransactionsSucceeded(ctx, outOfGasWithdrawalBlk, sepoliaFailedTransferBlockHash, []string{outOfGasWithdrawalHash})
	if err != nil {
		t.Fatal(err)
	}
	all, err := adapter.TransactionsSucceeded(ctx, outOfGasWithdrawalBlk, sepoliaFailedTransferBlockHash, hashes)
	if err != nil {
		t.Fatal(err)
	}
	if succeeded, known := single[outOfGasWithdrawalHash]; !known || succeeded {
		t.Fatalf("eth_getTransactionReceipt: %v", single)
	}
	if succeeded, known := all[outOfGasWithdrawalHash]; !known || succeeded || len(all) != len(hashes) {
		t.Fatalf("eth_getBlockReceipts: %d of %d statuses, failed transfer %v", len(all), len(hashes), succeeded)
	}

	_, err = adapter.GetTransactionBlock(ctx, outOfGasWithdrawalHash)
	var failed *chain.TransactionFailedError
	if !errors.As(err, &failed) || failed.Result != evmResultOutOfGas || failed.Block != outOfGasWithdrawalBlk {
		t.Fatalf("GetTransactionBlock: %v", err)
	}
	t.Logf("%d native transfers in block %d; the failed one is reported failed by both reads", len(hashes), outOfGasWithdrawalBlk)
}
