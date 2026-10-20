package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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
	reorgSenderNonce   = uint64(7)
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
	scans     int
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
	c.scans++
	block, ok := c.blocks[height]
	if !ok {
		return nil, fmt.Errorf("no block at %d", height)
	}
	return append([]types.DetectedTransfer(nil), block.transfers...), nil
}

func (c *reorgChain) scanCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scans
}

// reorgTransfer is a native transfer signed by reorgSender with reorgSenderNonce, as
// the EVM scan reports it.
func reorgTransfer(txHash, to string) types.DetectedTransfer {
	nonce := reorgSenderNonce
	return types.DetectedTransfer{
		TxHash: txHash, From: reorgSender, To: to, Amount: big.NewInt(5_000_000_000_000_000), Asset: reorgChainID,
		Sender: reorgSender, SenderNonce: &nonce,
	}
}

// spendCall is one NonceSpend question.
type spendCall struct {
	txHash, from     string
	nonce, fromBlock uint64
}

// evmReorgChain is reorgChain with what EVM adds: a nonce proof (NonceSpend) and
// a lookup on every endpoint (SearchTransactionBlock). nullReceipts makes the
// failover order answer no receipt for a mined transaction (an endpoint without a
// transaction index); indexed lets another endpoint find it.
type evmReorgChain struct {
	*reorgChain
	nullReceipts bool
	indexed      bool
	spend        chain.NonceSpend
	spendErr     error
	spendCalls   []spendCall
}

var (
	_ chain.NonceSpendReader    = (*evmReorgChain)(nil)
	_ chain.TransactionSearcher = (*evmReorgChain)(nil)
)

func (c *evmReorgChain) GetTransactionBlock(ctx context.Context, txHash string) (uint64, error) {
	if c.nullReceipts {
		return 0, nil
	}
	return c.reorgChain.GetTransactionBlock(ctx, txHash)
}

func (c *evmReorgChain) SearchTransactionBlock(ctx context.Context, txHash string) (uint64, error) {
	if c.nullReceipts && !c.indexed {
		return 0, nil
	}
	return c.reorgChain.GetTransactionBlock(ctx, txHash)
}

func (c *evmReorgChain) NonceSpend(_ context.Context, txHash, from string, nonce, fromBlock uint64) (chain.NonceSpend, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spendCalls = append(c.spendCalls, spendCall{txHash: txHash, from: from, nonce: nonce, fromBlock: fromBlock})
	return c.spend, c.spendErr
}

func (c *evmReorgChain) spends() []spendCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]spendCall(nil), c.spendCalls...)
}

// finalSlotChain is reorgChain on a chain whose scanner reads only final blocks
// (it satisfies the Solana port).
type finalSlotChain struct{ *reorgChain }

func (finalSlotChain) AssembleSolana(*types.UnsignedTx, []byte) (*types.SignedTx, error) {
	return nil, errors.New("not used")
}

// reorgFixture is the deposit service over reorgChain, with the production deposit
// event publisher and a config of the wallet's account that subscribes to every
// deposit event and delivers to receiver.
type reorgFixture struct {
	chain    *reorgChain
	svc      *Service
	webhooks *webhook.Service
	receiver *webhookReceiver
	clock    *testClock
}

func newReorgFixture(t *testing.T) reorgFixture {
	t.Helper()
	inner := newReorgChain()
	return newReorgFixtureOver(t, inner, inner)
}

func newEVMReorgFixture(t *testing.T) (reorgFixture, *evmReorgChain) {
	t.Helper()
	adapter := &evmReorgChain{reorgChain: newReorgChain()}
	return newReorgFixtureOver(t, adapter, adapter.reorgChain), adapter
}

// newReorgFixtureOver registers adapter, which reads inner's chain.
func newReorgFixtureOver(t *testing.T, adapter types.Chain, inner *reorgChain) reorgFixture {
	t.Helper()
	fixtures.TestDB(t)
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
	clock := &testClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	svc.now = clock.Now
	return reorgFixture{chain: inner, svc: svc, webhooks: webhookSvc, receiver: receiver, clock: clock}
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

// reorgMark is what raw_payload keeps of the reorganization check of a row.
func reorgMark(t *testing.T, tx models.Transaction) (seenAt, nextCheckAt int64) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(tx.RawPayload), &payload); err != nil {
		t.Fatalf("raw_payload %q: %v", tx.RawPayload, err)
	}
	seen, _ := payload["reorg_seen_at"].(float64)
	next, _ := payload["reorg_next_check_at"].(float64)
	return int64(seen), int64(next)
}

func (f reorgFixture) unresolved(t *testing.T) int {
	t.Helper()
	health := f.svc.countStaleOutbound(context.Background(), f.clock.Now())
	if health.Err != nil {
		t.Fatal(health.Err)
	}
	return health.ReorgedTotal
}

func (f reorgFixture) delivered(t *testing.T) []string {
	t.Helper()
	if _, err := f.webhooks.DeliverPending(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	return f.receiver.received()
}

// A reorganized transaction usually returns to the mempool and lands later, and the
// scanner never records a transaction twice: without a proof that it can never
// land, the deposit is left unconfirmed (counted, backed off), never failed, and it
// confirms from the block it lands in.
func TestRunConfirmationCheck_ATransactionInNoBlockIsNeverFailedWithoutProof(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	if events := f.delivered(t); len(events) != 1 || events[0] != string(types.EventDepositPending) {
		t.Fatalf("the receiver got %v, want deposit.pending", events)
	}
	f.chain.mine(reorgDepositBlock, "b")
	first := f.clock.Now()

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)

	got := reorgDeposit(t, reorgDepositTx)
	if seen, next := reorgMark(t, got); got.Status != string(types.TxStatusPending) || seen != first.Unix() || next != first.Add(time.Minute).Unix() {
		t.Fatalf("status %s mark (%d, %d), want pending, seen now and checked again in a minute", got.Status, seen, next)
	}
	if n := f.unresolved(t); n != 1 {
		t.Fatalf("health counts %d unresolved reorganized deposits, want 1", n)
	}
	for day := 0; day < 30; day++ {
		f.clock.advance(24 * time.Hour)
		f.pass(t, reorgDepositBlock+reorgRequiredConfs+uint64(day))
		if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusPending) {
			t.Fatalf("day %d: status %s, want the deposit left unconfirmed without a proof of loss", day, got.Status)
		}
	}
	if seen, next := reorgMark(t, reorgDeposit(t, reorgDepositTx)); seen != first.Unix() || next-f.clock.Now().Unix() != int64(time.Hour/time.Second) {
		t.Fatalf("mark (%d, %d), want the first sight kept and the next check an hour away at most", seen, next)
	}

	// The transaction lands, 40 blocks later.
	landed := uint64(reorgDepositBlock + 40)
	f.chain.mine(landed, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.clock.advance(time.Hour)
	f.pass(t, landed+reorgRequiredConfs-1)
	moved := reorgDeposit(t, reorgDepositTx)
	if seen, next := reorgMark(t, moved); moved.BlockNumber != int64(landed) || moved.BlockHash != reorgHash("b", landed) || seen != 0 || next != 0 {
		t.Fatalf("block %d hash %s mark (%d, %d), want the deposit moved to block %d and its mark cleared", moved.BlockNumber, moved.BlockHash, seen, next, landed)
	}
	if n := f.unresolved(t); n != 0 {
		t.Fatalf("health still counts %d unresolved", n)
	}
	f.pass(t, landed+reorgRequiredConfs-1)
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("status %s, want confirmed from the block it landed in", got.Status)
	}
	if events := f.delivered(t); len(events) != 2 || events[1] != string(types.EventDepositConfirmed) {
		t.Fatalf("the receiver got %v, want deposit.pending then deposit.confirmed, never deposit.failed", events)
	}
}

func TestRunConfirmationCheck_ADepositWhoseNonceAnotherTransactionSpentFailsReorgedOut(t *testing.T) {
	f, evm := newEVMReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	if events := f.delivered(t); len(events) != 1 {
		t.Fatalf("the receiver got %v, want deposit.pending", events)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(reorgDeposit(t, reorgDepositTx).RawPayload), &payload); err != nil ||
		payload["sender"] != reorgSender || payload["sender_nonce"] != float64(reorgSenderNonce) {
		t.Fatalf("raw_payload %v (%v), want the transaction's sender and nonce kept by the scan", payload, err)
	}
	f.chain.mine(reorgDepositBlock, "b")
	evm.spend = chain.NonceSpend{Spent: true, Block: reorgDepositBlock + 1}

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)

	got := reorgDeposit(t, reorgDepositTx)
	if got.Status != string(types.TxStatusFailed) || got.ErrorMessage != reorgedOutCode || got.ConfirmedAt != nil {
		t.Fatalf("status %s error %q confirmed_at %v, want failed with %s and never confirmed", got.Status, got.ErrorMessage, got.ConfirmedAt, reorgedOutCode)
	}
	if calls := evm.spends(); len(calls) != 1 || calls[0] != (spendCall{txHash: reorgDepositTx, from: reorgSender, nonce: reorgSenderNonce, fromBlock: reorgDepositBlock - 64}) {
		t.Fatalf("nonce proofs %+v, want one for the deposit's sender and nonce from 64 blocks under its block", calls)
	}
	failed := depositEventsFor(t, types.EventDepositFailed, got.ID)
	if len(failed) != 1 || len(depositEventsFor(t, types.EventDepositConfirmed, got.ID)) != 0 {
		t.Fatalf("%d deposit.failed and %d deposit.confirmed, want exactly one deposit.failed", len(failed), len(depositEventsFor(t, types.EventDepositConfirmed, got.ID)))
	}
	if body := failed[0]; body.Status != string(types.TxStatusFailed) || body.FailureCode == nil || *body.FailureCode != reorgedOutCode || body.ConfirmedAt != nil {
		t.Fatalf("deposit.failed payload %+v", body)
	}
	if events := f.delivered(t); len(events) != 2 || events[1] != string(types.EventDepositFailed) {
		t.Fatalf("the receiver got %v, want deposit.pending then deposit.failed", events)
	}
}

func TestRunConfirmationCheck_ANonceNotSpentYetIsCheckedAgainOnlyWhenItsBackoffEnds(t *testing.T) {
	f, evm := newEVMReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b")
	tip := uint64(reorgDepositBlock + reorgRequiredConfs)

	f.pass(t, tip)
	checks := func() (int, int) {
		reads, _ := f.chain.reads()
		return len(reads), len(evm.spends())
	}
	if reads, spends := checks(); reads != 1 || spends != 1 || reorgDeposit(t, reorgDepositTx).Status != string(types.TxStatusPending) {
		t.Fatalf("%d hash reads %d nonce proofs status %s", reads, spends, reorgDeposit(t, reorgDepositTx).Status)
	}

	f.clock.advance(time.Minute - time.Second)
	f.pass(t, tip+10)
	if reads, spends := checks(); reads != 1 || spends != 1 {
		t.Fatalf("one second before the next check: %d hash reads and %d nonce proofs, want none more", reads, spends)
	}
	f.clock.advance(time.Second)
	f.pass(t, tip+20)
	if reads, spends := checks(); reads != 2 || spends != 2 {
		t.Fatalf("at the next check: %d hash reads and %d nonce proofs, want one more each", reads, spends)
	}
	// Still unproven a minute after the first sight: the next check is a minute away.
	f.clock.advance(time.Minute - time.Second)
	f.pass(t, tip+30)
	if reads, spends := checks(); reads != 2 || spends != 2 {
		t.Fatalf("%d hash reads and %d nonce proofs before the second backoff ended", reads, spends)
	}
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusPending) || len(depositEventsFor(t, types.EventDepositFailed, got.ID)) != 0 {
		t.Fatalf("status %s, want pending and no deposit.failed while the nonce is not spent", got.Status)
	}
}

func TestRunConfirmationCheck_AnEndpointWithoutTransactionIndexIsAnsweredByTheNonceProof(t *testing.T) {
	f, evm := newEVMReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b")
	f.chain.mine(reorgDepositBlock+1, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))
	evm.nullReceipts = true
	evm.spend = chain.NonceSpend{Spent: true, Ours: true, Block: reorgDepositBlock + 1}

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
	if got := reorgDeposit(t, reorgDepositTx); got.BlockNumber != reorgDepositBlock+1 || got.BlockHash != reorgHash("b", reorgDepositBlock+1) {
		t.Fatalf("block %d hash %s, want the block where the nonce proof found it", got.BlockNumber, got.BlockHash)
	}
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("status %s, want confirmed: the value reached the address", got.Status)
	}
}

func TestRunConfirmationCheck_EveryEndpointIsAskedWhereAMovedTransactionIs(t *testing.T) {
	f, evm := newEVMReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b")
	f.chain.mine(reorgDepositBlock+1, "b", reorgTransfer(reorgDepositTx, reorgDepositTo))
	evm.nullReceipts, evm.indexed = true, true
	evm.spendErr = errors.New("the nonce proof is not needed")

	f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)

	if got := reorgDeposit(t, reorgDepositTx); got.BlockNumber != reorgDepositBlock+1 {
		t.Fatalf("block %d, want the block another endpoint knows", got.BlockNumber)
	}
	if calls := evm.spends(); len(calls) != 0 {
		t.Fatalf("nonce proofs %+v, want none for a transaction an endpoint found", calls)
	}
}

func TestRunConfirmationCheck_AHashThatMatchesAgainClearsTheReorgMark(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b") // a backend serving another fork for a while
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	if seen, _ := reorgMark(t, reorgDeposit(t, reorgDepositTx)); seen == 0 {
		t.Fatal("the mismatch left no mark")
	}

	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.clock.advance(time.Minute)
	f.pass(t, reorgDepositBlock+reorgRequiredConfs+1)

	got := reorgDeposit(t, reorgDepositTx)
	if seen, next := reorgMark(t, got); got.Status != string(types.TxStatusConfirmed) || seen != 0 || next != 0 {
		t.Fatalf("status %s mark (%d, %d), want confirmed with the mark cleared", got.Status, seen, next)
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

func TestRunConfirmationCheck_ACompetingBlockThatCreditsTheDepositDifferentlyLeavesItUnresolved(t *testing.T) {
	anotherAmount := reorgTransfer(reorgDepositTx, reorgDepositTo)
	anotherAmount.Amount = big.NewInt(1)
	anotherToken := reorgTransfer(reorgDepositTx, reorgDepositTo)
	anotherToken.Token = &types.Token{Symbol: "usdc", Contract: "0x00000000000000000000000000000000000000c1", Decimals: 6}
	cases := map[string]types.DetectedTransfer{
		"another amount":  anotherAmount,
		"another address": reorgTransfer(reorgDepositTx, reorgOtherTo),
		"another token":   anotherToken,
	}
	for name, competing := range cases {
		t.Run(name, func(t *testing.T) {
			f := newReorgFixture(t)
			f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
			f.recordDeposit(t, reorgDepositBlock)
			f.chain.mine(reorgDepositBlock, "b", competing)

			f.pass(t, reorgDepositBlock+reorgRequiredConfs-1)
			f.pass(t, reorgDepositBlock+reorgRequiredConfs)

			got := reorgDeposit(t, reorgDepositTx)
			if seen, _ := reorgMark(t, got); got.BlockHash != reorgHash("a", reorgDepositBlock) || got.Status != string(types.TxStatusPending) || seen == 0 {
				t.Fatalf("hash %s status %s seen %d: a block that credits %s must not move or confirm the deposit", got.BlockHash, got.Status, seen, name)
			}
		})
	}
}

func TestRunConfirmationCheck_AStuckRelocationReadsEachBlockOncePerPassAndIsBackedOff(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo), reorgTransfer(reorgOtherTx, reorgOtherTo))
	f.recordDeposit(t, reorgDepositBlock)
	first, second := reorgTransfer(reorgDepositTx, reorgDepositTo), reorgTransfer(reorgOtherTx, reorgOtherTo)
	first.Amount, second.Amount = big.NewInt(1), big.NewInt(2)
	f.chain.mine(reorgDepositBlock, "b", first, second)
	scans := f.chain.scanCount()

	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	if got := f.chain.scanCount() - scans; got != 1 {
		t.Fatalf("%d block reads for two deposits whose transactions are in the same block, want one", got)
	}
	_, lookups := f.chain.reads()

	f.clock.advance(30 * time.Second)
	f.pass(t, reorgDepositBlock+reorgRequiredConfs+1)
	if _, more := f.chain.reads(); f.chain.scanCount()-scans != 1 || more != lookups {
		t.Fatalf("a pass inside the backoff read %d blocks and %d transactions, want none", f.chain.scanCount()-scans-1, more-lookups)
	}
	if n := f.unresolved(t); n != 2 {
		t.Fatalf("health counts %d unresolved, want both", n)
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

func TestRunConfirmationCheck_AFinalSlotTheScannerRecordedIsNotReadAgain(t *testing.T) {
	inner := newReorgChain()
	f := newReorgFixtureOver(t, finalSlotChain{inner}, inner)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.chain.mine(reorgDepositBlock+1, "a", reorgTransfer(reorgOtherTx, reorgOtherTo))
	f.chain.setTip(reorgDepositBlock + 1)
	f.recordDeposit(t, reorgDepositBlock)
	f.recordDeposit(t, reorgDepositBlock+1)
	ingested := reorgDeposit(t, reorgOtherTx)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", ingested.ID).Update("source", models.TxSourceDepositIngest); err != nil {
		t.Fatal(err)
	}

	f.pass(t, reorgDepositBlock+reorgRequiredConfs+1)

	if reads, _ := f.chain.reads(); len(reads) != 1 || reads[0] != reorgDepositBlock+1 {
		t.Fatalf("hash reads %v, want one, of the block of the deposit the ingest recorded", reads)
	}
	for _, txHash := range []string{reorgDepositTx, reorgOtherTx} {
		if got := reorgDeposit(t, txHash); got.Status != string(types.TxStatusConfirmed) {
			t.Fatalf("%s: status %s", txHash, got.Status)
		}
	}
}

func TestSame_BlockHash_IsCaseBlindForHexAndExactOtherwise(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"0xAB12", "0xab12", true},
		{"28D45A55AB2C0C4EC4F4B6F305D75DE861FD4F675301EC1035622910CD14FB2D", "28d45a55ab2c0c4ec4f4b6f305d75de861fd4f675301ec1035622910cd14fb2d", true},
		{"Braetyz9fCW1P6ahjpxpBqLVDCh61kZPSNgZwx8okPt2", "braetyz9fcw1p6ahjpxpbqlvdch61kzpsngzwx8okpt2", false},
		{"Braetyz9fCW1P6ahjpxpBqLVDCh61kZPSNgZwx8okPt2", "Braetyz9fCW1P6ahjpxpBqLVDCh61kZPSNgZwx8okPt2", true},
	}
	for _, tc := range cases {
		if got := sameBlockHash(tc.a, tc.b); got != tc.same {
			t.Fatalf("sameBlockHash(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

// Two processes (the scan's confirmation step and the tracker) can read the same
// open row: the one that settles it first wins, and the other neither overwrites
// the status nor publishes its event.
func TestFail_Deposit_ADepositConfirmedMeanwhileIsNeitherFailedNorReported(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	stale := reorgDeposit(t, reorgDepositTx)
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)

	err := f.svc.failDeposit(context.Background(), stale, reorgedOutCode)

	if !errors.Is(err, models.ErrTransactionSettled) {
		t.Fatalf("err %v, want ErrTransactionSettled", err)
	}
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusConfirmed) || len(depositEventsFor(t, types.EventDepositFailed, got.ID)) != 0 {
		t.Fatalf("status %s, want confirmed and no deposit.failed", got.Status)
	}
}

func TestApply_Confirmations_ADepositFailedMeanwhileIsNeitherConfirmedNorReported(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo))
	f.recordDeposit(t, reorgDepositBlock)
	stale := reorgDeposit(t, reorgDepositTx)
	if err := f.svc.failDeposit(context.Background(), stale, reorgedOutCode); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.applyConfirmations(context.Background(), f.chain, reorgDepositBlock+reorgRequiredConfs, []models.Transaction{stale}); err != nil {
		t.Fatal(err)
	}

	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusFailed) || got.ConfirmedAt != nil || len(depositEventsFor(t, types.EventDepositConfirmed, got.ID)) != 0 {
		t.Fatalf("status %s confirmed_at %v, want it left failed and no deposit.confirmed", got.Status, got.ConfirmedAt)
	}
}

func TestReorged_Deposits_ListsTheUnresolvedAndFailsOneOnlyWithApply(t *testing.T) {
	f := newReorgFixture(t)
	f.chain.mine(reorgDepositBlock, "a", reorgTransfer(reorgDepositTx, reorgDepositTo), reorgTransfer(reorgOtherTx, reorgOtherTo))
	f.recordDeposit(t, reorgDepositBlock)
	f.chain.mine(reorgDepositBlock, "b", reorgTransfer(reorgOtherTx, reorgOtherTo))
	f.pass(t, reorgDepositBlock+reorgRequiredConfs)
	unresolved, settled := reorgDeposit(t, reorgDepositTx), reorgDeposit(t, reorgOtherTx)
	ctx := context.Background()

	listed, err := f.svc.ReorgedDeposits(ctx, ReorgedDepositsRequest{ChainID: reorgChainID})
	if err != nil || len(listed.Rows) != 1 || listed.Rows[0].TransactionID != unresolved.ID || listed.Rows[0].SeenAt.IsZero() {
		t.Fatalf("rows %+v err %v, want the one unresolved deposit", listed.Rows, err)
	}

	dry, err := f.svc.ReorgedDeposits(ctx, ReorgedDepositsRequest{ChainID: reorgChainID, Fail: unresolved.ID})
	if err != nil || dry.Failed {
		t.Fatalf("dry run %+v err %v", dry, err)
	}
	if got := reorgDeposit(t, reorgDepositTx); got.Status != string(types.TxStatusPending) {
		t.Fatalf("a dry run changed the deposit: %s", got.Status)
	}
	if _, refused := f.svc.ReorgedDeposits(ctx, ReorgedDepositsRequest{Fail: settled.ID, Apply: true}); !errors.Is(refused, ErrDepositNotReorganized) {
		t.Fatalf("failing a deposit with no unresolved reorganization: err %v", refused)
	}
	if _, unknown := f.svc.ReorgedDeposits(ctx, ReorgedDepositsRequest{Fail: uuid.New(), Apply: true}); unknown == nil {
		t.Fatal("an unknown deposit was failed")
	}

	applied, err := f.svc.ReorgedDeposits(ctx, ReorgedDepositsRequest{Fail: unresolved.ID, Apply: true})
	if err != nil || !applied.Failed {
		t.Fatalf("apply %+v err %v", applied, err)
	}
	got := reorgDeposit(t, reorgDepositTx)
	if got.Status != string(types.TxStatusFailed) || got.ErrorMessage != reorgedOutCode || len(depositEventsFor(t, types.EventDepositFailed, got.ID)) != 1 {
		t.Fatalf("status %s error %q, want failed reorged_out with one deposit.failed", got.Status, got.ErrorMessage)
	}
	if n := f.unresolved(t); n != 0 {
		t.Fatalf("health still counts %d", n)
	}
}
