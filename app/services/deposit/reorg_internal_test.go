package deposit

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
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

// receiptReorgChain is evmReorgChain with the receipt port the EVM adapter has
// (chain.ReceiptStatusReader), so the tracker reads a native deposit's receipt again
// before it confirms it. A transaction failedOnChain holds has a failed receipt on
// every read: the scan's, the lookups and GetTransactionBlock.
type receiptReorgChain struct{ *evmReorgChain }

var _ chain.ReceiptStatusReader = receiptReorgChain{}

func (c receiptReorgChain) TransactionsSucceeded(_ context.Context, _ uint64, _ string, txHashes []string) (map[string]bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	statuses := make(map[string]bool, len(txHashes))
	for _, hash := range txHashes {
		_, failed := c.failedOnChain[hash]
		statuses[strings.ToLower(hash)] = !failed
	}
	return statuses, nil
}

func newReceiptReorgFixture(t *testing.T) reorgFixture {
	t.Helper()
	adapter := receiptReorgChain{&evmReorgChain{reorgChain: newReorgChain()}}
	return newReorgFixtureOver(t, adapter, adapter.reorgChain)
}

// internalTransferRecordings are the two ways the push records an internal
// transfer: with its block's hash, or without one (checked by its transaction's block).
var internalTransferRecordings = map[string]string{
	"recorded with its block's hash": reorgHash("a", reorgDepositBlock),
	"recorded without a hash":        "",
}

// The transaction that carried an internal transfer can be mined and fail: its
// value never moved, and its block still holds it. Like a native deposit's, its
// receipt decides, so the deposit fails with the on-chain code instead of confirming.
func TestRunConfirmationCheck_AnInternalTransferWhoseReceiptFailedFailsOnChain(t *testing.T) {
	for name, recordedHash := range internalTransferRecordings {
		t.Run(name, func(t *testing.T) {
			f := newReceiptReorgFixture(t)
			f.recordInternalDeposit(t, recordedHash)
			f.chain.mine(reorgDepositBlock, "a", reorgContractCall())
			f.chain.failedOnChain[reorgDepositTx] = "reverted"

			f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
			f.pass(t, reorgDepositBlock+reorgRequiredConfs)

			got := reorgDeposit(t, reorgDepositTx)
			if got.Status != string(types.TxStatusFailed) || got.ErrorMessage != "onchain_reverted" || got.ConfirmedAt != nil {
				t.Fatalf("status %s error %q confirmed_at %v, want failed with onchain_reverted and never confirmed", got.Status, got.ErrorMessage, got.ConfirmedAt)
			}
			failed, confirmed := depositEventsFor(t, types.EventDepositFailed, got.ID), depositEventsFor(t, types.EventDepositConfirmed, got.ID)
			if len(failed) != 1 || len(confirmed) != 0 {
				t.Fatalf("%d deposit.failed and %d deposit.confirmed, want exactly one deposit.failed", len(failed), len(confirmed))
			}
			if body := failed[0]; body.FailureCode == nil || *body.FailureCode != "onchain_reverted" || body.LogIndex != reorgInternalPosition {
				t.Fatalf("deposit.failed payload %+v, want onchain_reverted at the internal position", body)
			}
		})
	}
}

// One whose receipt succeeded confirms, its receipt read before it does.
func TestRunConfirmationCheck_AnInternalTransferWhoseReceiptSucceededConfirms(t *testing.T) {
	// The transaction reads of the pass: the receipt, after the lookup of the
	// transaction's block for a deposit recorded without a hash.
	reads := map[string]int{"recorded with its block's hash": 1, "recorded without a hash": 2}
	for name, recordedHash := range internalTransferRecordings {
		t.Run(name, func(t *testing.T) {
			f := newReceiptReorgFixture(t)
			f.recordInternalDeposit(t, recordedHash)
			f.chain.mine(reorgDepositBlock, "a", reorgContractCall())
			_, before := f.chain.reads()

			f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)

			got := reorgDeposit(t, reorgDepositTx)
			if got.Status != string(types.TxStatusConfirmed) || got.BlockHash != reorgHash("a", reorgDepositBlock) || got.LogIndex != reorgInternalPosition {
				t.Fatalf("status %s hash %q position %d, want confirmed in its block at its position", got.Status, got.BlockHash, got.LogIndex)
			}
			if _, after := f.chain.reads(); after-before != reads[name] {
				t.Fatalf("%d transaction reads, want %d: the receipt is read before the deposit confirms", after-before, reads[name])
			}
			if len(depositEventsFor(t, types.EventDepositConfirmed, got.ID)) != 1 || len(depositEventsFor(t, types.EventDepositFailed, got.ID)) != 0 {
				t.Fatal("want exactly one deposit.confirmed and no deposit.failed")
			}
		})
	}
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

// trackedState is what a tracker pass may write on a deposit row.
type trackedState struct {
	status, blockHash, rawPayload, errorMessage string
	blockNumber                                 int64
	confirmations, position                     int
	confirmed                                   bool
}

func trackedStateOf(tx models.Transaction) trackedState {
	return trackedState{
		status: tx.Status, blockHash: tx.BlockHash, rawPayload: tx.RawPayload, errorMessage: tx.ErrorMessage,
		blockNumber: tx.BlockNumber, confirmations: tx.Confirmations, position: tx.LogIndex, confirmed: tx.ConfirmedAt != nil,
	}
}

func webhookEventCount(t *testing.T) int64 {
	t.Helper()
	count, err := facades.Orm().Query().Model(&models.WebhookEvent{}).Count()
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// A deposit recorded without a hash, an internal transfer in its recorded block,
// confirms only once its block's hash is recorded for it. When that fails (the hash
// is not read, or another process changed the row after this pass read it), the
// pass leaves the deposit unconfirmed, as it found it, and publishes nothing; a
// later pass confirms it.
func TestRunConfirmationCheck_AnInternalTransferWhoseBlockHashIsNotRecordedIsNotConfirmed(t *testing.T) {
	cases := map[string]struct {
		// hashErr fails the read of the block's hash; meanwhile is what another
		// process writes on the row while the pass reads that hash.
		hashErr   error
		meanwhile map[string]any
	}{
		"its block's hash is not read": {hashErr: errors.Join(chain.ErrProviderUnavailable, errors.New("503"))},
		"another pass recorded its block's hash meanwhile": {
			meanwhile: map[string]any{"block_hash": reorgHash("a", reorgDepositBlock)},
		},
		"another pass counted its confirmations meanwhile": {
			meanwhile: map[string]any{"status": string(types.TxStatusConfirming), "confirmations": reorgRequiredConfs - 1},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f, _ := newEVMReorgFixture(t)
			found := f.recordInternalDeposit(t, "")
			f.chain.mine(reorgDepositBlock, "a", reorgContractCall())
			f.chain.failHashReads(tc.hashErr)
			if tc.meanwhile != nil {
				var once sync.Once
				f.chain.setOnHashRead(func(uint64) {
					once.Do(func() {
						if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", found.ID).Update(tc.meanwhile); err != nil {
							t.Error(err)
						}
						found = reorgDeposit(t, reorgDepositTx)
					})
				})
			}
			events := webhookEventCount(t)

			f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)

			if got, want := trackedStateOf(reorgDeposit(t, reorgDepositTx)), trackedStateOf(found); got != want {
				t.Fatalf("deposit %+v, want it unconfirmed as the pass found it %+v", got, want)
			}
			if found.Status == string(types.TxStatusConfirmed) {
				t.Fatal("the deposit was confirmed before the pass wrote")
			}
			if n := webhookEventCount(t); n != events {
				t.Fatalf("%d webhook events, want the %d there were: nothing published", n, events)
			}

			f.chain.failHashReads(nil)
			f.chain.setOnHashRead(nil)
			f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
			if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) || got.BlockHash != reorgHash("a", reorgDepositBlock) {
				t.Fatalf("next pass: status %s hash %q, want confirmed in its block", got.Status, got.BlockHash)
			}
		})
	}
}
