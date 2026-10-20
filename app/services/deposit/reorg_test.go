package deposit

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	reorgChainID       = "eth"
	reorgDepositBlock  = 100
	reorgRequiredConfs = 3
	reorgDepositTx     = "0x00000000000000000000000000000000000000000000000000000000000000e1"
	reorgOtherTx       = "0x00000000000000000000000000000000000000000000000000000000000000e2"
	reorgDepositTo     = "0x00000000000000000000000000000000000000e0"
	reorgOtherTo       = "0x00000000000000000000000000000000000000e3"
	reorgSender        = "0x00000000000000000000000000000000000000f0"
	reorgedOutCode     = "reorged_out"
)

// reorgHash is a block hash of fork at height: two forks never share a hash.
func reorgHash(fork string, height uint64) string {
	return fmt.Sprintf("0x%s%062x", fork, height)
}

// fakeBlock is what the fake chain holds at one height.
type fakeBlock struct {
	hash      string
	transfers []types.DetectedTransfer
}

// reorgChain is an eth adapter over a canonical chain the test rewrites: a
// reorganization swaps the block at a height for another one, which may or may
// not hold the deposit's transaction. BlockHashAt, GetTransactionBlock and
// ScanBlock all read that one chain.
type reorgChain struct {
	*mocks.MockChain
	mu        sync.Mutex
	tip       uint64
	blocks    map[uint64]fakeBlock
	hashErr   error
	hashReads []uint64
	txReads   int
	// failedOnChain is the on-chain failure result of a transaction mined in a
	// block whose receipt failed (EVM), by hash.
	failedOnChain map[string]string
}

var _ chain.BlockHashReader = (*reorgChain)(nil)

func newReorgChain() *reorgChain {
	adapter := mocks.NewMockChain(reorgChainID)
	adapter.RequiredConfirmationsVal = reorgRequiredConfs
	return &reorgChain{MockChain: adapter, tip: reorgDepositBlock, blocks: map[uint64]fakeBlock{}, failedOnChain: map[string]string{}}
}

// mine puts a block of fork at height, holding transfers, in the canonical chain.
func (c *reorgChain) mine(height uint64, fork string, transfers ...types.DetectedTransfer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hash := reorgHash(fork, height)
	held := make([]types.DetectedTransfer, 0, len(transfers))
	for _, transfer := range transfers {
		transfer.BlockNumber, transfer.BlockHash = height, hash
		held = append(held, transfer)
	}
	c.blocks[height] = fakeBlock{hash: hash, transfers: held}
}

func (c *reorgChain) setTip(height uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tip = height
}

func (c *reorgChain) failHashReads(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hashErr = err
}

func (c *reorgChain) reads() ([]uint64, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uint64(nil), c.hashReads...), c.txReads
}

func (c *reorgChain) GetLatestBlock(context.Context) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tip, nil
}

func (c *reorgChain) BlockHashAt(_ context.Context, height uint64) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hashReads = append(c.hashReads, height)
	if c.hashErr != nil {
		return "", c.hashErr
	}
	block, ok := c.blocks[height]
	if !ok {
		return "", fmt.Errorf("no block at %d", height)
	}
	return block.hash, nil
}

func (c *reorgChain) GetTransactionBlock(_ context.Context, txHash string) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.txReads++
	for height, block := range c.blocks {
		for _, transfer := range block.transfers {
			if transfer.TxHash != txHash {
				continue
			}
			if result, failed := c.failedOnChain[txHash]; failed {
				return 0, fmt.Errorf("tx %s: %w", txHash, &chain.TransactionFailedError{Result: result, Block: height})
			}
			return height, nil
		}
	}
	return 0, nil
}

func (c *reorgChain) ScanBlock(_ context.Context, height uint64) ([]types.DetectedTransfer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	block, ok := c.blocks[height]
	if !ok {
		return nil, fmt.Errorf("no block at %d", height)
	}
	return append([]types.DetectedTransfer(nil), block.transfers...), nil
}

func reorgTransfer(txHash, to string) types.DetectedTransfer {
	return types.DetectedTransfer{TxHash: txHash, From: reorgSender, To: to, Amount: big.NewInt(5_000_000_000_000_000), Asset: reorgChainID}
}

// reorgFixture is the deposit service over reorgChain, with the production deposit
// event publisher and a config of the wallet's account that subscribes to every
// deposit event and delivers to receiver.
type reorgFixture struct {
	chain    *reorgChain
	svc      *Service
	webhooks *webhook.Service
	receiver *webhookReceiver
}

func newReorgFixture(t *testing.T) reorgFixture {
	t.Helper()
	fixtures.TestDB(t)
	adapter := newReorgChain()
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)

	account := fixtures.InsertAccount(t, "reorg deposit owner")
	wallet := fixtures.InsertWalletWithAccount(t, reorgChainID, &account.ID)
	fixtures.InsertAddress(t, wallet.ID, reorgChainID, reorgDepositTo, "player-reorg", 1)
	fixtures.InsertAddress(t, wallet.ID, reorgChainID, reorgOtherTo, "player-other", 2)
	receiver := &webhookReceiver{}
	server := httptest.NewServer(receiver)
	t.Cleanup(server.Close)
	fixtures.InsertScopedWebhookConfig(t, server.URL, depositHookSecret, []string{
		string(types.EventDepositPending), string(types.EventDepositConfirming),
		string(types.EventDepositConfirmed), string(types.EventDepositFailed),
	}, &account.ID, nil)

	webhookSvc := newWebhookSvc()
	svc := newDepositSvc(registry, webhookSvc)
	svc.SetDepositEvents(newDepositPublisher(webhookSvc, chainAssetDecimals{reorgChainID + "/" + reorgChainID: etherDecimals}))
	return reorgFixture{chain: adapter, svc: svc, webhooks: webhookSvc, receiver: receiver}
}

// recordDeposit has the scanner record the deposits of the block at height.
func (f reorgFixture) recordDeposit(t *testing.T, height uint64) {
	t.Helper()
	if _, err := f.svc.ScanBlock(context.Background(), reorgChainID, height); err != nil {
		t.Fatal(err)
	}
}

func (f reorgFixture) pass(t *testing.T, tip uint64) {
	t.Helper()
	f.chain.setTip(tip)
	if err := f.svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func reorgDeposit(t *testing.T, txHash string) models.Transaction {
	t.Helper()
	var deposit models.Transaction
	if err := facades.Orm().Query().Where("tx_type", models.TxTypeDeposit).Where("tx_hash", txHash).FirstOrFail(&deposit); err != nil {
		t.Fatalf("deposit %s: %v", txHash, err)
	}
	return deposit
}

func (f reorgFixture) delivered(t *testing.T) []string {
	t.Helper()
	if _, err := f.webhooks.DeliverPending(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	return f.receiver.received()
}

func TestRunConfirmationCheck_ADepositWhoseBlockWasReorganizedAwayFailsAndIsNeverCredited(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	if events := f.delivered(t); len(events) != 1 || events[0] != string(types.EventDepositPending) {
		t.Fatalf("the receiver got %v, want deposit.pending", events)
	}
	// The block is swapped for one without the transaction, which is in no other block.
	f.chain.mine(reorgDepositBlock, "b")

	confirmingTip := uint64(reorgDepositBlock + reorgRequiredConfs - 1)
	f.pass(t, confirmingTip)
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusPending) || got.ConfirmedAt != nil {
		t.Fatalf("seen gone once: status %s, want it left unconfirmed until the chain moved on", got.Status)
	}

	f.pass(t, confirmingTip+reorgRequiredConfs-1)
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusPending) {
		t.Fatalf("status %s before the chain moved %d blocks past the first sight", got.Status, reorgRequiredConfs)
	}

	f.pass(t, confirmingTip+reorgRequiredConfs)
	f.pass(t, confirmingTip+reorgRequiredConfs+1)

	got := reorgDeposit(t, reorgDepositTx)
	if got.Status != string(types.TxStatusFailed) || got.ErrorMessage != reorgedOutCode || got.ConfirmedAt != nil {
		t.Fatalf("status %s error %q confirmed_at %v, want failed with %s and never confirmed", got.Status, got.ErrorMessage, got.ConfirmedAt, reorgedOutCode)
	}
	failed := depositEventsFor(t, types.EventDepositFailed, got.ID)
	if len(failed) != 1 || len(depositEventsFor(t, types.EventDepositConfirmed, got.ID)) != 0 {
		t.Fatalf("%d deposit.failed and %d deposit.confirmed, want exactly one deposit.failed", len(failed), len(depositEventsFor(t, types.EventDepositConfirmed, got.ID)))
	}
	if body := failed[0]; body.Status != string(types.TxStatusFailed) || body.FailureCode == nil || *body.FailureCode != reorgedOutCode || body.ConfirmedAt != nil {
		t.Fatalf("deposit.failed payload %+v", body)
	}
	if events := f.delivered(t); len(events) != 2 || events[0] != string(types.EventDepositPending) || events[1] != string(types.EventDepositFailed) {
		t.Fatalf("the receiver got %v, want deposit.pending then deposit.failed", events)
	}
}

func TestRunConfirmationCheck_ADepositMovedToAnotherBlockConfirmsFromItsNewBlock(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	// The new fork mined the transaction one block later.
	f.chain.mine(reorgDepositBlock, "b")
	f.chain.mine(reorgDepositBlock+1, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
	moved := reorgDeposit(t, reorgDepositTx)
	if moved.BlockNumber != reorgDepositBlock+1 || moved.BlockHash != reorgHash("b", reorgDepositBlock+1) || moved.Status == string(types.TxStatusConfirmed) {
		t.Fatalf("block %d hash %s status %s, want the new block recorded and the deposit not confirmed yet", moved.BlockNumber, moved.BlockHash, moved.Status)
	}

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirming) || got.Confirmations != reorgRequiredConfs-1 {
		t.Fatalf("status %s confirmations %d, want confirming at the new block's depth", got.Status, got.Confirmations)
	}

	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	got := reorgDeposit(t, reorgDepositTx)
	if got.Status != string(types.TxStatusConfirmed) || got.Confirmations != reorgRequiredConfs || got.BlockNumber != reorgDepositBlock+1 {
		t.Fatalf("status %s confirmations %d block %d, want confirmed from block %d", got.Status, got.Confirmations, got.BlockNumber, reorgDepositBlock+1)
	}
	if len(depositEventsFor(t, types.EventDepositConfirmed, got.ID)) != 1 || len(depositEventsFor(t, types.EventDepositFailed, got.ID)) != 0 {
		t.Fatal("want one deposit.confirmed and no deposit.failed")
	}
	if reads, _ := f.chain.reads(); len(reads) != 2 || reads[0] != reorgDepositBlock || reads[1] != reorgDepositBlock+1 {
		t.Fatalf("hash reads %v, want the old height once and the new height once, each when the deposit would confirm", reads)
	}
}

func TestRunConfirmationCheck_ACompetingBlockThatHoldsTheSameTransactionConfirmsTheDepositUnderItsHash(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
	if got := reorgDeposit(t, reorgDepositTx); got.BlockNumber != reorgDepositBlock || got.BlockHash != reorgHash("b", reorgDepositBlock) || got.Status == string(types.TxStatusConfirmed) {
		t.Fatalf("block %d hash %s status %s, want the competing block's hash recorded and the deposit confirmed only after it is checked", got.BlockNumber, got.BlockHash, got.Status)
	}
	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("status %s, want confirmed in the competing block", got.Status)
	}
}

func TestRunConfirmationCheck_ADepositWhoseTransactionWasMinedAgainAndFailedFailsWithTheOnChainCode(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b")
	f.chain.mine(reorgDepositBlock+1, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.chain.failedOnChain[reorgDepositTx] = "reverted"

	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	f.pass(t, reorgDepositBlock+reorgRequiredConfs+1)

	got := reorgDeposit(t, reorgDepositTx)
	if got.Status != string(types.TxStatusFailed) || got.ErrorMessage != "onchain_reverted" {
		t.Fatalf("status %s error %q, want failed with onchain_reverted", got.Status, got.ErrorMessage)
	}
	if len(depositEventsFor(t, types.EventDepositFailed, got.ID)) != 1 || len(depositEventsFor(t, types.EventDepositConfirmed, got.ID)) != 0 {
		t.Fatal("want exactly one deposit.failed and no deposit.confirmed")
	}
}

func TestRunConfirmationCheck_ADepositSeenGoneThatIsMinedAgainIsMovedNotFailed(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b")
	tip := uint64(reorgDepositBlock + reorgRequiredConfs - 1)
	f.pass(t, tip)

	// Back from the mempool, two blocks above the tip of the first sight.
	f.chain.mine(tip+2, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.pass(t, tip+reorgRequiredConfs)

	got := reorgDeposit(t, reorgDepositTx)
	if got.Status == string(types.TxStatusFailed) || got.BlockNumber != int64(tip+2) || got.BlockHash != reorgHash("b", tip+2) {
		t.Fatalf("status %s block %d, want the deposit moved to block %d", got.Status, got.BlockNumber, tip+2)
	}
	f.pass(t, tip+2+reorgRequiredConfs-1)
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("status %s, want confirmed from its new block", got.Status)
	}
}

func TestRunConfirmationCheck_ACanonicalHashThatCannotBeReadLeavesTheDepositUnconfirmed(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo), reorgTransfer(reorgOtherTx, reorgOtherTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.failHashReads(errors.Join(chain.ErrProviderUnavailable, errors.New("503")))

	f.pass(t, reorgDepositBlock+reorgRequiredConfs)

	for _, txHash := range []string{reorgDepositTx, reorgOtherTx} {
		if got := reorgDeposit(t, txHash); got.Status == string(types.TxStatusConfirmed) || got.Status == string(types.TxStatusFailed) {
			t.Fatalf("%s: status %s, want it left for the next pass", txHash, got.Status)
		}
	}
	reads, txReads := f.chain.reads()
	if len(reads) != 1 || txReads != 0 {
		t.Fatalf("hash reads %v and %d transaction reads: two deposits of one block read its hash once a pass, and an error proves nothing", reads, txReads)
	}

	f.chain.failHashReads(nil)
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	for _, txHash := range []string{reorgDepositTx, reorgOtherTx} {
		if got := reorgDeposit(t, txHash); got.Status != string(types.TxStatusConfirmed) {
			t.Fatalf("%s: status %s, want confirmed once the hash is read and matches", txHash, got.Status)
		}
	}
	if reads, txReads := f.chain.reads(); len(reads) != 2 || txReads != 0 {
		t.Fatalf("hash reads %v and %d transaction reads, want one hash read per pass and no relocation", reads, txReads)
	}
}

func TestRunConfirmationCheck_ADepositWithoutARecordedBlockHashIsNotChecked(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	deposit := reorgDeposit(t, reorgDepositTx)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", deposit.ID).Update("block_hash", ""); err != nil {
		t.Fatal(err)
	}

	f.pass(t, reorgDepositBlock+reorgRequiredConfs)

	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("status %s, want confirmed as before: there is no hash to compare", got.Status)
	}
	if reads, _ := f.chain.reads(); len(reads) != 0 {
		t.Fatalf("hash reads %v, want none", reads)
	}
}
