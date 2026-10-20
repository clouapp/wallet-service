package deposit

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
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
			f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
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
