package deposit

import (
	"math/big"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// reorgInternalPosition is a position the Alchemy push gives an internal transfer
	// (native value a contract sends): below types.NativeValuePosition.
	reorgInternalPosition = types.NativeValuePosition - 1 - 12345
	// reorgContract is the unwatched contract the deposit's transaction calls.
	reorgContract = "0x00000000000000000000000000000000000000c0"
)

// reorgContractCall is the deposit transaction as the scanner reads it when the
// deposit is an internal transfer: its top-level call pays the contract, and no
// transfer it reports credits the deposit's address.
func reorgContractCall() types.DetectedTransfer {
	call := reorgTransfer(reorgDepositTx, reorgContract)
	call.Amount = big.NewInt(1)
	return call
}

// recordInternalDeposit records the deposit as the push records an internal
// transfer: from the ingest, at an internal position, with blockHash as the hash.
func (f reorgFixture) recordInternalDeposit(t *testing.T, blockHash string) models.Transaction {
	t.Helper()
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	deposit := reorgDeposit(t, reorgDepositTx)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", deposit.ID).
		Update(map[string]any{"block_hash": blockHash, "source": models.TxSourceDepositIngest, "log_index": reorgInternalPosition}); err != nil {
		t.Fatal(err)
	}
	return reorgDeposit(t, reorgDepositTx)
}

// An internal transfer is in no block the scanner reads, so the push's deposit,
// recorded without a hash, is checked by its transaction's block: in the recorded
// block it takes that block's hash and confirms by depth and its receipt, as before
// the reorganization check.
func TestRunConfirmationCheck_AnInternalTransferRecordedWithoutAHashConfirmsByItsTransactionsBlock(t *testing.T) {
	f, _ := newEVMReorgFixture(t)
	f.recordInternalDeposit(t, "")
	f.chain.mine(reorgDepositBlock, "a", reorgContractCall())

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)

	got := reorgDeposit(t, reorgDepositTx)
	if got.Status != string(types.TxStatusConfirmed) || got.BlockHash != reorgHash("a", reorgDepositBlock) ||
		got.BlockNumber != reorgDepositBlock || got.LogIndex != reorgInternalPosition {
		t.Fatalf("deposit status %s, block %d %q, position %d; want confirmed in its block under its hash, at its position",
			got.Status, got.BlockNumber, got.BlockHash, got.LogIndex)
	}
	if seen, next := reorgMark(t, got); seen != 0 || next != 0 || f.unresolved(t) != 0 {
		t.Fatalf("reorganization mark %d/%d, %d unresolved; want none", seen, next, f.unresolved(t))
	}
}

// One whose transaction is in another block moves there, keeping its position, and
// confirms from that block's depth on a later pass.
func TestRunConfirmationCheck_AnInternalTransferRecordedWithoutAHashMovesToItsTransactionsBlock(t *testing.T) {
	f, _ := newEVMReorgFixture(t)
	f.recordInternalDeposit(t, "")
	f.chain.mine(reorgDepositBlock, "b")
	f.chain.mine(reorgDepositBlock+1, "b", reorgContractCall())

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)

	moved := reorgDeposit(t, reorgDepositTx)
	if moved.Status == string(types.TxStatusConfirmed) || moved.BlockNumber != reorgDepositBlock+1 ||
		moved.BlockHash != reorgHash("b", reorgDepositBlock+1) || moved.LogIndex != reorgInternalPosition {
		t.Fatalf("deposit status %s, block %d %q, position %d; want it moved to block %d at its position, unconfirmed",
			moved.Status, moved.BlockNumber, moved.BlockHash, moved.LogIndex, reorgDepositBlock+1)
	}
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("status %s at the new block's depth, want confirmed", got.Status)
	}
}

// One recorded with a hash whose block was reorganized away moves with its
// transaction too, though no transfer the scanner reads credits it.
func TestRunConfirmationCheck_AReorganizedInternalTransferMovesWithItsTransaction(t *testing.T) {
	f, _ := newEVMReorgFixture(t)
	f.recordInternalDeposit(t, reorgHash("a", reorgDepositBlock))
	f.chain.mine(reorgDepositBlock, "b", reorgContractCall())

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)

	moved := reorgDeposit(t, reorgDepositTx)
	if moved.Status == string(types.TxStatusConfirmed) || moved.BlockHash != reorgHash("b", reorgDepositBlock) || moved.LogIndex != reorgInternalPosition {
		t.Fatalf("deposit status %s, hash %q, position %d; want it moved to the competing block at its position, unconfirmed",
			moved.Status, moved.BlockHash, moved.LogIndex)
	}
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) || f.unresolved(t) != 0 {
		t.Fatalf("status %s, %d unresolved; want confirmed and none", got.Status, f.unresolved(t))
	}
}

// One whose transaction is in no block is never confirmed.
func TestRunConfirmationCheck_AnInternalTransferWhoseTransactionIsInNoBlockWaits(t *testing.T) {
	f, _ := newEVMReorgFixture(t)
	f.recordInternalDeposit(t, "")
	f.chain.mine(reorgDepositBlock, "b")

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)

	if got := reorgDeposit(t, reorgDepositTx); got.Status == string(types.TxStatusConfirmed) || got.Status == string(types.TxStatusFailed) || got.BlockHash != "" {
		t.Fatalf("deposit status %s, hash %q; want it unconfirmed, unfailed and unmoved", got.Status, got.BlockHash)
	}
}
