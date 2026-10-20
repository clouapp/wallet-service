package deposit

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/types"
)

const reorgUSDT = "0xdac17f958d2ee523a2206206994597c13d831ec7"

// reorgTokenTransfer is a USDT Transfer log of the deposit transaction at a
// block-wide position (an EVM log index differs from one block to another).
func reorgTokenTransfer(position int) types.DetectedTransfer {
	token := types.Token{Symbol: models.SymbolUSDT, Contract: reorgUSDT, Decimals: 6, ChainID: reorgChainID}
	return types.DetectedTransfer{TxHash: reorgDepositTx, From: reorgSender, To: reorgDepositTo,
		Amount: big.NewInt(10_000_000), Asset: token.Symbol, Token: &token, LogIndex: position}
}

func reorgDepositRows(t *testing.T) []models.Transaction {
	t.Helper()
	var rows []models.Transaction
	if err := facades.Orm().Query().Where("tx_type", models.TxTypeDeposit).Where("tx_hash", reorgDepositTx).Order("log_index").Find(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// A deposit moved to the block its transaction was mined in again takes its
// transfer's position there, so a later scan of that block (the pending retry,
// scan:deposits --block or --tx) or the provider's push finds it recorded and
// records nothing again.
func TestRunConfirmationCheck_AMovedDepositTakesItsTransfersPositionInTheNewBlock(t *testing.T) {
	for name, sameHeight := range map[string]bool{"competing block at the same height": true, "mined again later": false} {
		t.Run(name, func(t *testing.T) {
			f := newReorgFixture(t)
			f.svc.registry.RegisterToken(types.Token{Symbol: models.SymbolUSDT, Contract: reorgUSDT, Decimals: 6, ChainID: reorgChainID})
			f.chain.mine(reorgDepositBlock, "a", reorgTokenTransfer(3))
			f.recordDeposit(t, reorgDepositBlock)
			newHeight := uint64(reorgDepositBlock)
			if sameHeight {
				f.chain.mine(reorgDepositBlock, "b", reorgTokenTransfer(12))
			} else {
				f.chain.mine(reorgDepositBlock, "b")
				newHeight = reorgDepositBlock + 1
				f.chain.mine(newHeight, "b", reorgTokenTransfer(12))
			}

			f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
			moved := reorgDepositRows(t)
			if len(moved) != 1 || moved[0].BlockNumber != int64(newHeight) || moved[0].BlockHash != reorgHash("b", newHeight) || moved[0].LogIndex != 12 {
				t.Fatalf("rows %+v, want the deposit moved to block %d at position 12", moved, newHeight)
			}

			if _, err := f.svc.ScanBlock(context.Background(), reorgChainID, newHeight); err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.ScanTransaction(context.Background(), reorgChainID, reorgDepositTx); err != nil {
				t.Fatal(err)
			}
			err := newIngestSvc(f.svc.registry).Ingest(context.Background(), ingest.InboundEvent{ChainID: reorgChainID, Transfers: []providers.InboundTransfer{{
				TxHash: reorgDepositTx, BlockNumber: newHeight, BlockHash: reorgHash("b", newHeight), From: reorgSender, To: reorgDepositTo,
				Amount: big.NewInt(10_000_000), Asset: "USDT", Token: &types.Token{Contract: reorgUSDT, Decimals: 6}, LogIndex: 12,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			if rows := reorgDepositRows(t); len(rows) != 1 || rows[0].ID != moved[0].ID {
				t.Fatalf("%d deposit rows for one transfer after the rescans and the push, want the moved one alone: %+v", len(rows), rows)
			}
		})
	}
}

// Two transfers of one transaction that credit the same amount to the same address
// are two deposits; each moves to a position of the new block nobody else holds.
func TestRunConfirmationCheck_TwoLikeTransfersMoveToTwoPositions(t *testing.T) {
	f := newReorgFixture(t)
	f.svc.registry.RegisterToken(types.Token{Symbol: models.SymbolUSDT, Contract: reorgUSDT, Decimals: 6, ChainID: reorgChainID})
	f.chain.mine(reorgDepositBlock, "a", reorgTokenTransfer(3), reorgTokenTransfer(4))
	f.recordDeposit(t, reorgDepositBlock)
	if rows := reorgDepositRows(t); len(rows) != 2 {
		t.Fatalf("%d rows, want two deposits", len(rows))
	}
	f.chain.mine(reorgDepositBlock, "b", reorgTokenTransfer(12), reorgTokenTransfer(13))

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)

	rows := reorgDepositRows(t)
	if len(rows) != 2 || rows[0].LogIndex != 12 || rows[1].LogIndex != 13 || rows[0].BlockHash != reorgHash("b", reorgDepositBlock) || rows[1].BlockHash != reorgHash("b", reorgDepositBlock) {
		t.Fatalf("rows %+v, want both moved to the competing block at positions 12 and 13", rows)
	}
	if _, err := f.svc.ScanBlock(context.Background(), reorgChainID, reorgDepositBlock); err != nil {
		t.Fatal(err)
	}
	if rows := reorgDepositRows(t); len(rows) != 2 {
		t.Fatalf("%d rows after a rescan, want two", len(rows))
	}
}

// A deposit the provider push recorded without a block hash (Alchemy native
// transfers, Helius) is not confirmed by its depth alone: its block is read first
// and must credit it, which records the block's hash, and it confirms after the
// usual check.
func TestRunConfirmationCheck_AnIngestDepositWithoutABlockHashIsCheckedBeforeItConfirms(t *testing.T) {
	for name, finalSlots := range map[string]bool{"EVM": false, "Solana (finalized slots)": true} {
		t.Run(name, func(t *testing.T) {
			inner := newReorgChain()
			var f reorgFixture
			if finalSlots {
				f = newReorgFixtureOver(t, finalSlotChain{inner}, inner)
			} else {
				f = newReorgFixtureOver(t, inner, inner)
			}
			// The native value of the transaction, as an EVM push records it.
			native := reorgTransfer(reorgDepositTx, reorgDepositTo)
			native.LogIndex = types.NativeValuePosition
			f.chain.mine(reorgDepositBlock, "a", native)
			f.recordDeposit(t, reorgDepositBlock)
			deposit := reorgDeposit(t, reorgDepositTx)
			if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", deposit.ID).
				Update(map[string]any{"block_hash": "", "source": models.TxSourceDepositIngest}); err != nil {
				t.Fatal(err)
			}

			f.pass(t, reorgDepositBlock+reorgRequiredConfs+5)
			if got := reorgDeposit(t, reorgDepositTx); got.Status == string(types.TxStatusConfirmed) || got.BlockHash != reorgHash("a", reorgDepositBlock) {
				t.Fatalf("status %s hash %q, want the block read and its hash recorded before any confirmation", got.Status, got.BlockHash)
			}
			f.pass(t, reorgDepositBlock+reorgRequiredConfs+5)
			if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) {
				t.Fatalf("status %s, want confirmed once its recorded hash is the chain's", got.Status)
			}
		})
	}
}

func TestRunConfirmationCheck_AnIngestDepositWithoutABlockHashWhoseBlockDoesNotHoldItWaits(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	deposit := reorgDeposit(t, reorgDepositTx)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", deposit.ID).
		Update(map[string]any{"block_hash": "", "source": models.TxSourceDepositIngest}); err != nil {
		t.Fatal(err)
	}
	f.chain.mine(reorgDepositBlock, "b")

	f.pass(t, reorgDepositBlock+reorgRequiredConfs+5)

	got := reorgDeposit(t, reorgDepositTx)
	if seen, _ := reorgMark(t, got); got.Status == string(types.TxStatusConfirmed) || got.Status == string(types.TxStatusFailed) || got.BlockHash != "" || seen == 0 {
		t.Fatalf("status %s hash %q seen %d, want it held unresolved", got.Status, got.BlockHash, seen)
	}
}

// The scanner always records the block hash: a scanner row without one (a fixture,
// a hand edit) is not checked and confirms by its depth as before.
func TestRunConfirmationCheck_AScannerDepositWithoutABlockHashIsNotChecked(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	deposit := reorgDeposit(t, reorgDepositTx)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", deposit.ID).Update("block_hash", ""); err != nil {
		t.Fatal(err)
	}
	scans := f.chain.scanCount()

	f.pass(t, reorgDepositBlock+reorgRequiredConfs)

	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("status %s, want confirmed as before", got.Status)
	}
	if reads, _ := f.chain.reads(); len(reads) != 0 || f.chain.scanCount() != scans {
		t.Fatalf("hash reads %v and %d block reads, want none", reads, f.chain.scanCount()-scans)
	}
}

// A deposit another process settled or moved since this pass read it is not moved
// again from the stale copy.
func TestMove_Deposit_LeavesARowChangedSinceThePassReadIt(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	stale := reorgDeposit(t, reorgDepositTx)
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	f.chain.mine(reorgDepositBlock+1, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))

	err := f.svc.moveDeposit(context.Background(), newReorgPass(f.chain), stale, reorgDepositBlock+1)

	if !errors.Is(err, models.ErrTransactionSettled) {
		t.Fatalf("err %v, want ErrTransactionSettled", err)
	}
	if got := reorgDeposit(t, reorgDepositTx); got.BlockNumber != reorgDepositBlock || got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("block %d status %s, want the confirmed row left where it was", got.BlockNumber, got.Status)
	}
}

// A move decides under the transaction's deposit lock, the one the scanner and the
// push take (depositrecord), so it never races them on the transaction's rows.
func TestMove_Deposit_WaitsForTheTransactionsDepositLock(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	stale := reorgDeposit(t, reorgDepositTx)
	f.chain.mine(reorgDepositBlock+1, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))
	holder := f.svc.txRepo
	holding, release, held := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		held <- holder.Within(context.Background(), func(ctx context.Context) error {
			if err := holder.LockTransactionDeposits(ctx, reorgChainID, reorgDepositTx); err != nil {
				close(holding)
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	moved := make(chan error, 1)
	go func() {
		moved <- f.svc.moveDeposit(context.Background(), newReorgPass(f.chain), stale, reorgDepositBlock+1)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for waiting := int64(0); waiting == 0; {
		select {
		case err := <-moved:
			close(release)
			t.Fatalf("the move finished (%v) while another recorder held the transaction's lock", err)
		default:
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("the move never waited for the transaction's lock")
		}
		if err := facades.Orm().Query().Raw(`SELECT COUNT(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&waiting); err != nil {
			close(release)
			t.Fatal(err)
		}
	}
	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if err := <-moved; err != nil {
		t.Fatalf("move after the lock was released: %v", err)
	}
	if got := reorgDeposit(t, reorgDepositTx); got.BlockNumber != reorgDepositBlock+1 {
		t.Fatalf("block %d, want the deposit moved once the lock was free", got.BlockNumber)
	}
}

// A nonce proof that cannot be made, a lookup that fails: nothing is proven, the
// deposit stays unresolved and is never failed.
func TestRunConfirmationCheck_AFailedProofOrLookupProvesNothing(t *testing.T) {
	cases := map[string]func(*evmReorgChain){
		"the nonce proof fails": func(evm *evmReorgChain) { evm.spendErr = errors.New("missing trie node") },
		"the lookup fails":      func(evm *evmReorgChain) { evm.lookupErr = errors.New("503 from every endpoint") },
	}
	for name, setUp := range cases {
		t.Run(name, func(t *testing.T) {
			f, evm := newEVMReorgFixture(t)
			f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
			f.recordDeposit(t, reorgDepositBlock)
			f.chain.mine(reorgDepositBlock, "b")
			setUp(evm)

			f.pass(t, reorgDepositBlock+reorgRequiredConfs)

			got := reorgDeposit(t, reorgDepositTx)
			if seen, _ := reorgMark(t, got); got.Status != string(types.TxStatusPending) || seen == 0 || len(depositEventsFor(t, types.EventDepositFailed, got.ID)) != 0 {
				t.Fatalf("status %s seen %d, want it unresolved, never failed", got.Status, seen)
			}
		})
	}
}

// The nonce proof finds our own transaction, mined again and failed on chain: the
// deposit fails with the on-chain code, it is not moved to that block.
func TestRunConfirmationCheck_OurTransactionFoundFailedByTheNonceProofFailsTheDeposit(t *testing.T) {
	f, evm := newEVMReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b")
	f.chain.mine(reorgDepositBlock+1, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))
	evm.nullReceipts = true
	evm.spend = chain.NonceSpend{Spent: true, Ours: true, Block: reorgDepositBlock + 1, Result: "reverted"}

	f.pass(t, reorgDepositBlock+reorgRequiredConfs)

	got := reorgDeposit(t, reorgDepositTx)
	if got.Status != string(types.TxStatusFailed) || got.ErrorMessage != "onchain_reverted" || got.BlockNumber != reorgDepositBlock {
		t.Fatalf("status %s error %q block %d, want failed with onchain_reverted where it was", got.Status, got.ErrorMessage, got.BlockNumber)
	}
}

const reorgReplacementTx = "0x00000000000000000000000000000000000000000000000000000000000000e9"

// The sender replaced the deposit's transaction (a speed-up with the same nonce,
// payee and amount) and the competing fork mined the replacement: the original can
// never land and fails reorged_out, and the replacement, which did pay the address,
// is recorded and confirms.
func TestRunConfirmationCheck_AReplacementThatPaysTheAddressInTheCompetingBlockIsRecorded(t *testing.T) {
	f, evm := newEVMReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b", reorgTransfer(reorgReplacementTx, reorgDepositTo))
	evm.spend = chain.NonceSpend{Spent: true, Block: reorgDepositBlock}

	f.pass(t, reorgDepositBlock+reorgRequiredConfs)

	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusFailed) || got.ErrorMessage != reorgedOutCode {
		t.Fatalf("original %s %q, want failed reorged_out", got.Status, got.ErrorMessage)
	}
	replacement := reorgDeposit(t, reorgReplacementTx)
	if replacement.BlockHash != reorgHash("b", reorgDepositBlock) || replacement.Amount != "5000000000000000" || replacement.ToAddress != reorgDepositTo {
		t.Fatalf("replacement %+v, want it recorded from the competing block", replacement)
	}
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	if got := reorgDeposit(t, reorgReplacementTx); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("replacement %s, want confirmed", got.Status)
	}
}

// The scanner moved its checkpoint past the reorganized height on the old fork: a
// reorganization found by a deposit's check rescans the new fork from that height
// up to the checkpoint, so a deposit only the new fork holds is recorded; what is
// above the checkpoint is the scanner's.
func TestRunConfirmationCheck_AReorganizationRescansTheNewForkUpToTheCheckpoint(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	if err := f.svc.saveCheckpoint(context.Background(), reorgChainID, reorgDepositBlock+5); err != nil {
		t.Fatal(err)
	}
	f.chain.mine(reorgDepositBlock, "b", reorgTransfer(reorgDepositTx, reorgDepositTo), reorgTransfer(reorgForkTx, reorgOtherTo))
	f.chain.mine(reorgDepositBlock+2, "b", reorgTransfer(reorgOtherTx, reorgOtherTo))
	f.chain.mine(reorgDepositBlock+7, "b", reorgTransfer(reorgReplacementTx, reorgOtherTo))

	f.pass(t, reorgDepositBlock+reorgRequiredConfs+5)

	for _, want := range []struct {
		tx    string
		block uint64
	}{{reorgForkTx, reorgDepositBlock}, {reorgOtherTx, reorgDepositBlock + 2}} {
		if got := reorgDeposit(t, want.tx); recordedBlock(got) != want.block || got.BlockHash != reorgHash("b", want.block) {
			t.Fatalf("deposit of the new fork %+v, want it recorded by the rescan in block %d", got, want.block)
		}
	}
	var above int64
	if err := countDepositRows(reorgReplacementTx, &above); err != nil || above != 0 {
		t.Fatalf("%d rows (%v) for a block above the checkpoint, want it left to the scanner", above, err)
	}
	if rows := reorgDepositRows(t); len(rows) != 1 || rows[0].BlockHash != reorgHash("b", reorgDepositBlock) {
		t.Fatalf("rows of the deposit %+v, want one, moved to the competing block", rows)
	}
}

// reorgForkTx pays a watched address in the new fork's block at the deposit's own
// height, which the rescan starts at.
const reorgForkTx = "0x00000000000000000000000000000000000000000000000000000000000000e4"

// The reorganization is marked before the new fork is recorded again: a pass that
// ends during that rescan (a Lambda timeout) leaves the mark, and the next pass
// does not start the rescan again.
func TestRunConfirmationCheck_APassThatEndsDuringTheRescanDoesNotStartItAgain(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	if err := f.svc.saveCheckpoint(context.Background(), reorgChainID, reorgDepositBlock+5); err != nil {
		t.Fatal(err)
	}
	for height := uint64(reorgDepositBlock); height <= reorgDepositBlock+5; height++ {
		f.chain.mine(height, "b")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.chain.setOnScan(func(uint64) { cancel() })
	f.chain.setTip(reorgDepositBlock + reorgRequiredConfs + 5)

	_ = f.svc.RunConfirmationCheck(ctx)

	f.chain.setOnScan(nil)
	if seen, _ := reorgMark(t, reorgDeposit(t, reorgDepositTx)); seen == 0 {
		t.Fatal("no reorganization mark after a pass that ended during the rescan")
	}
	scans := f.chain.scanCount()
	f.clock.advance(2 * time.Hour)
	f.pass(t, reorgDepositBlock+reorgRequiredConfs+6)
	if again := f.chain.scanCount() - scans; again != 0 {
		t.Fatalf("%d blocks read by the next pass, want no rescan", again)
	}
}

// The rescans of a pass share a time budget: the heights they do not reach go to
// the pending retry, which records them.
func TestRunConfirmationCheck_TheRescanLeavesWhatItsBudgetDoesNotReachToThePendingRetry(t *testing.T) {
	f := newReorgFixture(t)
	store, storeErr := pending.NewFileStore(pending.FileStoreDeps{Dir: t.TempDir()})
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	f.svc.SetPendingStore(store)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	if err := f.svc.saveCheckpoint(context.Background(), reorgChainID, reorgDepositBlock+5); err != nil {
		t.Fatal(err)
	}
	for height := uint64(reorgDepositBlock); height <= reorgDepositBlock+5; height++ {
		f.chain.mine(height, "b")
	}
	f.chain.mine(reorgDepositBlock+4, "b", reorgTransfer(reorgOtherTx, reorgOtherTo))
	// A block already pending keeps its own entry.
	failed := pending.Entry{Chain: reorgChainID, Block: reorgDepositBlock + 3, ErrorClass: pending.ClassRPC, LastError: "rpc down",
		Attempts: 2, FirstFailedAt: f.clock.Now(), LastFailedAt: f.clock.Now(), NextRetryAt: f.clock.Now().Add(time.Hour)}
	if err := store.Put(context.Background(), failed); err != nil {
		t.Fatal(err)
	}
	f.chain.setOnScan(func(uint64) { f.clock.advance(reorgRescanBudget / 2) })

	f.pass(t, reorgDepositBlock+reorgRequiredConfs+5)

	f.chain.setOnScan(nil)
	var early int64
	if err := countDepositRows(reorgOtherTx, &early); err != nil || early != 0 {
		t.Fatalf("%d rows (%v) for a height past the budget, want it left to the pending retry", early, err)
	}
	entries, listErr := f.svc.ListPending(context.Background(), reorgChainID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	var heights []uint64
	for _, entry := range entries {
		heights = append(heights, entry.Block)
	}
	slices.Sort(heights)
	if want := []uint64{reorgDepositBlock + 2, reorgDepositBlock + 3, reorgDepositBlock + 4, reorgDepositBlock + 5}; !slices.Equal(heights, want) {
		t.Fatalf("pending heights %v, want %v", heights, want)
	}
	for _, entry := range entries {
		if entry.Block == failed.Block && (entry.Attempts != failed.Attempts || entry.LastError != failed.LastError) {
			t.Fatalf("entry of a block already pending %+v, want it kept", entry)
		}
	}
	if _, err := f.svc.ReprocessDuePending(context.Background(), reorgChainID); err != nil {
		t.Fatal(err)
	}
	if got := reorgDeposit(t, reorgOtherTx); got.BlockNumber != reorgDepositBlock+4 {
		t.Fatalf("deposit of the new fork %+v, want it recorded by the pending retry", got)
	}
}

// The budget is the pass's, not each rescan's: a second reorganized deposit, far
// from the first, rescans nothing once the first spent it.
func TestRunConfirmationCheck_TheRescansOfAPassShareOneBudget(t *testing.T) {
	f := newReorgFixture(t)
	store, storeErr := pending.NewFileStore(pending.FileStoreDeps{Dir: t.TempDir()})
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	f.svc.SetPendingStore(store)
	far := uint64(reorgDepositBlock + reorgRescanLimit + 50)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.chain.mine(far, "a", reorgTransfer(reorgOtherTx, reorgOtherTo))
	f.chain.setTip(far)
	f.recordDeposit(t, reorgDepositBlock)
	f.recordDeposit(t, far)
	if err := f.svc.saveCheckpoint(context.Background(), reorgChainID, far+5); err != nil {
		t.Fatal(err)
	}
	for height := uint64(reorgDepositBlock); height <= far+5; height++ {
		f.chain.mine(height, "b")
	}
	scans := f.chain.scanCount()
	f.chain.setOnScan(func(uint64) { f.clock.advance(reorgRescanBudget / 4) })

	f.pass(t, far+reorgRequiredConfs+5)

	f.chain.setOnScan(nil)
	if read := f.chain.scanCount() - scans; read != 4 {
		t.Fatalf("%d blocks read by the pass, want the 4 one budget allows", read)
	}
}

func countDepositRows(txHash string, out *int64) error {
	count, err := facades.Orm().Query().Model(&models.Transaction{}).Where("tx_type", models.TxTypeDeposit).Where("tx_hash", txHash).Count()
	*out = count
	return err
}

// confirmingMidway is the production publisher, except that while deposit.failed is
// staged another process confirms the deposit (the window between the failure's
// read of the row and its write).
type confirmingMidway struct {
	*depositevents.Publisher
	id uuid.UUID
}

func (c confirmingMidway) Stage(ctx context.Context, eventType types.EventType, tx models.Transaction) (func(context.Context), error) {
	now := time.Now().UTC()
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", c.id).
		Update(map[string]any{"status": string(types.TxStatusConfirmed), "confirmed_at": now}); err != nil {
		return nil, err
	}
	return c.Publisher.Stage(ctx, eventType, tx)
}

// committedAtSend is the production publisher, except that its staged send first
// reads the deposit through another connection: what it reads is what is committed
// when deposit.failed is sent.
type committedAtSend struct {
	*depositevents.Publisher
	id   uuid.UUID
	seen *string
}

func (c committedAtSend) Stage(ctx context.Context, eventType types.EventType, tx models.Transaction) (func(context.Context), error) {
	send, err := c.Publisher.Stage(ctx, eventType, tx)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) {
		var row models.Transaction
		if err := facades.Orm().Query().Where("id = ?", c.id).FirstOrFail(&row); err == nil {
			*c.seen = row.Status
		}
		send(ctx)
	}, nil
}

// deposit.failed is sent only once the failure is committed.
func TestFail_Deposit_SendsDepositFailedOnlyOnceTheFailureIsCommitted(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	deposit := reorgDeposit(t, reorgDepositTx)
	var seen string
	f.svc.SetDepositEvents(committedAtSend{Publisher: newDepositPublisher(f.webhooks, chainAssetDecimals{reorgChainID + "/" + reorgChainID: etherDecimals}), id: deposit.ID, seen: &seen})

	if err := f.svc.failDeposit(context.Background(), deposit, reorgedOutCode); err != nil {
		t.Fatal(err)
	}

	if seen != string(types.TxStatusFailed) {
		t.Fatalf("deposit read as %q when deposit.failed was sent, want %q", seen, types.TxStatusFailed)
	}
}

// deposit.failed and the failed status are written in one transaction, the status
// only from the status the failure read: a deposit confirmed in between keeps its
// status and no deposit.failed is left in the outbox.
func TestFail_Deposit_ADepositConfirmedWhileItsFailureIsWrittenKeepsNoFailure(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	deposit := reorgDeposit(t, reorgDepositTx)
	f.svc.SetDepositEvents(confirmingMidway{Publisher: newDepositPublisher(f.webhooks, chainAssetDecimals{reorgChainID + "/" + reorgChainID: etherDecimals}), id: deposit.ID})

	err := f.svc.failDeposit(context.Background(), deposit, reorgedOutCode)

	if !errors.Is(err, models.ErrTransactionSettled) {
		t.Fatalf("err %v, want ErrTransactionSettled", err)
	}
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) || len(depositEventsFor(t, types.EventDepositFailed, got.ID)) != 0 {
		t.Fatalf("status %s and %d deposit.failed rows, want confirmed and none", got.Status, len(depositEventsFor(t, types.EventDepositFailed, got.ID)))
	}
}

// With a publisher that cannot stage, deposit.failed is published before the row is
// written: the row read again first keeps a deposit settled meanwhile from being
// reported failed.
func TestFail_Deposit_APublisherThatCannotStageReportsNothingForASettledDeposit(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	stale := reorgDeposit(t, reorgDepositTx)
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	events := &recordingDepositEvents{}
	f.svc.SetDepositEvents(events)

	err := f.svc.failDeposit(context.Background(), stale, reorgedOutCode)

	if !errors.Is(err, models.ErrTransactionSettled) || len(events.events) != 0 {
		t.Fatalf("err %v and %v published, want ErrTransactionSettled and nothing", err, events.events)
	}
}
