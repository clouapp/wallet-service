package deposit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/macrowallets/waas/tests/testutil"
)

const (
	failingBlock  = 1003
	healthyBlock  = 1006
	scanHead      = 1008
	startBlock    = 1000
	failingTx     = "tx-db-down"
	healthyTx     = "tx-healthy"
	alwaysFail    = -1
	dbDownMessage = "insert: dial tcp 127.0.0.1:5433: connect: connection refused"
)

// flakyTxRepo fails Create for chosen transactions: N times, or always with alwaysFail.
type flakyTxRepo struct {
	*repositories.TransactionRepository
	mu       sync.Mutex
	failures map[string]int
	creates  map[string]int
}

func newFlakyTxRepo() *flakyTxRepo {
	return &flakyTxRepo{TransactionRepository: repositories.NewTransactionRepository(nil), failures: map[string]int{}, creates: map[string]int{}}
}

func (r *flakyTxRepo) failCreate(txHash string, times int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures[txHash] = times
}

func (r *flakyTxRepo) heal() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = map[string]int{}
}

func (r *flakyTxRepo) Create(ctx context.Context, tx *models.Transaction) error {
	r.mu.Lock()
	r.creates[tx.TxHash]++
	remaining := r.failures[tx.TxHash]
	if remaining > 0 {
		r.failures[tx.TxHash] = remaining - 1
	}
	r.mu.Unlock()
	if remaining != 0 {
		return errors.New(dbDownMessage)
	}
	return r.TransactionRepository.Create(ctx, tx)
}

// racingTxRepo reports no recorded deposit, as a process that checked just before
// another one inserted the same transaction would see.
type racingTxRepo struct {
	*repositories.TransactionRepository
}

func (racingTxRepo) CountByChainAndTxHash(context.Context, string, string, string) (int64, error) {
	return 0, nil
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type failureFixture struct {
	scanFixture
	txRepo *flakyTxRepo
	clock  *testClock
}

func newFailureFixture(t *testing.T, store pending.Store) failureFixture {
	t.Helper()
	f := newScanFixture(t, scanHead, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4}).withRedisCheckpoint(t, startBlock)
	txRepo := newFlakyTxRepo()
	f.svc.txRepo = txRepo
	clock := &testClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	f.svc.now = clock.Now
	if store != nil {
		f.svc.SetPendingStore(store)
	}
	f.adapter.depositTo(failingBlock, failingTx, f.address)
	f.adapter.depositTo(healthyBlock, healthyTx, f.address)
	return failureFixture{scanFixture: f, txRepo: txRepo, clock: clock}
}

type pendingBackends struct {
	store  *pending.DurableStore
	rdb    *redis.Client
	prefix string
	dir    string
}

func newPendingBackends(t *testing.T) pendingBackends {
	t.Helper()
	rdb := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, rdb)
	dir := t.TempDir()
	return pendingBackends{store: openPendingStore(t, rdb, prefix, dir), rdb: rdb, prefix: prefix, dir: dir}
}

// openPendingStore builds the store as the API does at boot, from the same Redis keys
// and directory, so a second call stands in for an API restart.
func openPendingStore(t *testing.T, rdb *redis.Client, prefix, dir string) *pending.DurableStore {
	t.Helper()
	var redisStore *pending.RedisStore
	if rdb != nil {
		var err error
		if redisStore, err = pending.NewRedisStore(rdb, prefix); err != nil {
			t.Fatal(err)
		}
	}
	fileStore, err := pending.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := pending.NewDurableStore(redisStore, fileStore)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func unreachableRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newPendingDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pending")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// breakDir replaces the directory with a regular file, so every file operation fails.
func breakDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func depositCount(t *testing.T, txHash string) int64 {
	t.Helper()
	count, err := facades.Orm().Query().Model(&models.Transaction{}).
		Where("chain", scanTestChain).Where("tx_hash", txHash).Where("tx_type", models.TxTypeDeposit).Count()
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func pendingEntries(t *testing.T, store pending.Store) []pending.Entry {
	t.Helper()
	entries, err := store.List(context.Background(), scanTestChain)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// captureLogs routes slog to a buffer for the test and returns its JSON records.
func captureLogs(t *testing.T) func() []map[string]any {
	t.Helper()
	var buffer bytes.Buffer
	var mu sync.Mutex
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&lockedWriter{mu: &mu, w: &buffer}, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		var records []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
			var record map[string]any
			if json.Unmarshal([]byte(line), &record) == nil {
				records = append(records, record)
			}
		}
		return records
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestFailurePolicyFromSettings(t *testing.T) {
	cases := []struct {
		name                                   string
		retries, delayMs, pending, max, newCap int
		want                                   FailurePolicy
		wantErr                                string
	}{
		{name: "zeros keep the defaults", want: DefaultFailurePolicy()},
		{name: "explicit values", retries: 2, delayMs: 50, pending: 10, max: 600, newCap: 3,
			want: FailurePolicy{ImmediateRetries: 2, ImmediateRetryDelay: 50 * time.Millisecond, PendingRetryDelay: 10 * time.Second, PendingRetryMaxDelay: 10 * time.Minute, MaxNewPendingPerCycle: 3}},
		{name: "a pending delay above the default cap raises the cap", pending: 7200,
			want: FailurePolicy{ImmediateRetries: 3, ImmediateRetryDelay: 100 * time.Millisecond, PendingRetryDelay: 2 * time.Hour, PendingRetryMaxDelay: 2 * time.Hour, MaxNewPendingPerCycle: 10}},
		{name: "explicit cap below the pending delay", pending: 600, max: 60, wantErr: "must not be below"},
		{name: "negative value", retries: -1, wantErr: "must not be negative"},
		{name: "too many retries", retries: MaxImmediateRetries + 1, wantErr: "between 0 and"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FailurePolicyFromSettings(tc.retries, tc.delayMs, tc.pending, tc.max, tc.newCap)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestFailurePolicy_Backoffs(t *testing.T) {
	policy := DefaultFailurePolicy()
	if got := fmt.Sprint(policy.immediateDelays()); got != "[100ms 300ms 900ms]" {
		t.Fatalf("immediate delays %s, want [100ms 300ms 900ms]", got)
	}
	for attempts, want := range map[int]time.Duration{1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 7: 30 * time.Minute, 50: 30 * time.Minute} {
		if got := policy.pendingBackoff(attempts); got != want {
			t.Errorf("attempt %d: backoff %s, want %s", attempts, got, want)
		}
	}
}

func TestScanLatestBlocks_TransientWriteFailureIsFixedByTheImmediateRetry(t *testing.T) {
	backends := newPendingBackends(t)
	f := newFailureFixture(t, backends.store)
	f.txRepo.failCreate(failingTx, 2)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != scanHead {
		t.Fatalf("expected the checkpoint at the head, got %d", got)
	}
	if depositCount(t, failingTx) != 1 || depositCount(t, healthyTx) != 1 {
		t.Fatal("both deposits must be recorded once")
	}
	if got := fmt.Sprint(f.sleeps.recorded()); got != "[100ms 300ms]" {
		t.Fatalf("expected two immediate retries at 100 ms and 300 ms, waited %s", got)
	}
	if entries := pendingEntries(t, backends.store); len(entries) != 0 {
		t.Fatalf("a block fixed by the immediate retry must not go pending, got %+v", entries)
	}
}

func TestScanLatestBlocks_TransientFetchFailureIsRefetched(t *testing.T) {
	backends := newPendingBackends(t)
	f := newFailureFixture(t, backends.store)
	f.adapter.failNextFetches(failingBlock, 2)
	f.txRepo.heal()

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if f.checkpoint(t) != scanHead || depositCount(t, failingTx) != 1 {
		t.Fatalf("expected the refetched block recorded and the checkpoint at the head, checkpoint %d", f.checkpoint(t))
	}
	if entries := pendingEntries(t, backends.store); len(entries) != 0 {
		t.Fatalf("unexpected pending entries %+v", entries)
	}
}

func TestScanLatestBlocks_PersistentFailureGoesPendingAndTheCheckpointAdvances(t *testing.T) {
	logs := captureLogs(t)
	backends := newPendingBackends(t)
	f := newFailureFixture(t, backends.store)
	f.txRepo.failCreate(failingTx, alwaysFail)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatalf("a block saved as pending must not fail the cycle: %v", err)
	}
	if got := f.checkpoint(t); got != scanHead {
		t.Fatalf("expected the checkpoint past the pending block, got %d", got)
	}
	if depositCount(t, healthyTx) != 1 {
		t.Fatal("a later deposit must not wait for the failing block")
	}
	if depositCount(t, failingTx) != 0 {
		t.Fatal("the failing deposit cannot be recorded yet")
	}
	if got := fmt.Sprint(f.sleeps.recorded()); got != "[100ms 300ms 900ms]" {
		t.Fatalf("expected three immediate retries, waited %s", got)
	}
	entries := pendingEntries(t, backends.store)
	if len(entries) != 1 {
		t.Fatalf("expected one pending entry, got %+v", entries)
	}
	entry := entries[0]
	start := f.clock.Now()
	if entry.Chain != scanTestChain || entry.Block != failingBlock || entry.Attempts != 1 || entry.ErrorClass != pending.ClassDatabase ||
		strings.Join(entry.TxHashes, ",") != failingTx || !strings.Contains(entry.LastError, "connection refused") ||
		!entry.FirstFailedAt.Equal(start) || !entry.LastFailedAt.Equal(start) || !entry.NextRetryAt.Equal(start.Add(DefaultPendingRetryDelay)) {
		t.Fatalf("unexpected pending entry %+v", entry)
	}

	var alert map[string]any
	for _, record := range logs() {
		if record["msg"] == "deposit block pending" {
			alert = record
		}
	}
	if alert == nil || alert["level"] != "ERROR" || alert["chain"] != scanTestChain || alert["block"] != float64(failingBlock) || alert["attempt"] != float64(1) {
		t.Fatalf("expected one structured ERROR alert with chain, block and attempt, got %v", alert)
	}
}

func TestReprocessPending_BacksOffThenRecoversWithoutDuplicates(t *testing.T) {
	backends := newPendingBackends(t)
	f := newFailureFixture(t, backends.store)
	f.txRepo.failCreate(failingTx, alwaysFail)
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}

	if resolved, err := f.svc.ReprocessDuePending(context.Background(), scanTestChain); err != nil || resolved != 0 {
		t.Fatalf("nothing is due before the backoff, got %d, %v", resolved, err)
	}

	f.clock.advance(DefaultPendingRetryDelay)
	result, err := f.svc.ReprocessPending(context.Background(), scanTestChain, false)
	if err != nil || result.Due != 1 || result.StillPending != 1 {
		t.Fatalf("expected the due block to fail again, got %+v, %v", result, err)
	}
	entry := pendingEntries(t, backends.store)[0]
	if entry.Attempts != 2 || !entry.NextRetryAt.Equal(f.clock.Now().Add(2*DefaultPendingRetryDelay)) {
		t.Fatalf("expected attempt 2 with a doubled backoff, got %+v", entry)
	}

	f.txRepo.heal()
	f.clock.advance(2 * DefaultPendingRetryDelay)
	result, err = f.svc.ReprocessPending(context.Background(), scanTestChain, false)
	if err != nil || result.Resolved != 1 || result.StillPending != 0 {
		t.Fatalf("expected the block recovered, got %+v, %v", result, err)
	}
	if entries := pendingEntries(t, backends.store); len(entries) != 0 {
		t.Fatalf("a recovered block must leave the pending list, got %+v", entries)
	}

	if _, err := f.svc.ScanBlock(context.Background(), scanTestChain, failingBlock); err != nil {
		t.Fatal(err)
	}
	if result, err := f.svc.ReprocessPending(context.Background(), scanTestChain, true); err != nil || result.Due != 0 {
		t.Fatalf("nothing may be left to retry, got %+v, %v", result, err)
	}
	if depositCount(t, failingTx) != 1 || depositCount(t, healthyTx) != 1 {
		t.Fatal("each deposit must be recorded exactly once")
	}
	if n := f.events.count(types.EventDepositPending); n != 2 {
		t.Fatalf("expected one deposit.pending per deposit, got %d", n)
	}
}

func TestScanLatestBlocks_RedisDownRecordsThePendingBlockInTheFile(t *testing.T) {
	dir := newPendingDir(t)
	store := openPendingStore(t, unreachableRedisClient(t), "test:unreachable:", dir)
	f := newFailureFixture(t, store)
	f.txRepo.failCreate(failingTx, alwaysFail)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatalf("the file alone must be enough to save the pending block: %v", err)
	}
	if got := f.checkpoint(t); got != scanHead {
		t.Fatalf("expected the checkpoint past the pending block, got %d", got)
	}
	fileOnly, err := pending.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if entries := pendingEntries(t, fileOnly); len(entries) != 1 || entries[0].Block != failingBlock {
		t.Fatalf("expected the block in the file, got %+v", entries)
	}
}

func TestScanLatestBlocks_PendingStoreDownKeepsTheCheckpointBeforeTheBlock(t *testing.T) {
	dir := newPendingDir(t)
	store := openPendingStore(t, unreachableRedisClient(t), "test:unreachable:", dir)
	breakDir(t, dir)
	f := newFailureFixture(t, store)
	f.txRepo.failCreate(failingTx, alwaysFail)

	err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain)
	if err == nil || !strings.Contains(err.Error(), "could not be recorded nor saved as pending") {
		t.Fatalf("expected the cycle to stop at the unsaved block, got %v", err)
	}
	if got := f.checkpoint(t); got != failingBlock-1 {
		t.Fatalf("expected the checkpoint right before block %d, got %d", failingBlock, got)
	}
	if depositCount(t, healthyTx) != 0 {
		t.Fatal("no block after the unsaved one may be recorded in this cycle")
	}

	f.txRepo.heal()
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if f.checkpoint(t) != scanHead || depositCount(t, failingTx) != 1 || depositCount(t, healthyTx) != 1 {
		t.Fatal("the next cycle must retry the block and continue")
	}
}

func TestScanLatestBlocks_StopsWhenTooManyBlocksFailInOneCycle(t *testing.T) {
	backends := newPendingBackends(t)
	f := newFailureFixture(t, backends.store)
	policy := DefaultFailurePolicy()
	policy.MaxNewPendingPerCycle = 1
	if err := f.svc.SetFailurePolicy(policy); err != nil {
		t.Fatal(err)
	}
	f.txRepo.failCreate(failingTx, alwaysFail)
	f.txRepo.failCreate(healthyTx, alwaysFail)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err == nil {
		t.Fatal("expected the cycle to stop at the second failing block")
	}
	if got := f.checkpoint(t); got != healthyBlock-1 {
		t.Fatalf("expected the checkpoint right before the second failing block, got %d", got)
	}
	if entries := pendingEntries(t, backends.store); len(entries) != 1 || entries[0].Block != failingBlock {
		t.Fatalf("only the first failing block may go pending, got %+v", entries)
	}
}

func TestPendingBlocks_SurviveAnAPIRestart(t *testing.T) {
	backends := newPendingBackends(t)
	f := newFailureFixture(t, backends.store)
	f.txRepo.failCreate(failingTx, alwaysFail)
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}

	restarted := newDepositSvc(f.svc.registry, newWebhookSvc())
	restarted.SetPendingStore(openPendingStore(t, backends.rdb, backends.prefix, backends.dir))
	restarted.sleep = f.sleeps.sleep
	entries, err := restarted.ListPending(context.Background(), scanTestChain)
	if err != nil || len(entries) != 1 || entries[0].Block != failingBlock {
		t.Fatalf("expected the pending block after a restart, got %+v, %v", entries, err)
	}

	keys, err := backends.rdb.Keys(context.Background(), backends.prefix+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) == 0 {
		t.Fatal("expected the entry in Redis too")
	}
	if err := backends.rdb.Del(context.Background(), keys...).Err(); err != nil {
		t.Fatal(err)
	}
	fileOnlyRestart := newDepositSvc(f.svc.registry, newWebhookSvc())
	fileOnlyRestart.SetPendingStore(openPendingStore(t, backends.rdb, backends.prefix, backends.dir))
	fileOnlyRestart.sleep = f.sleeps.sleep
	result, err := fileOnlyRestart.ReprocessPending(context.Background(), scanTestChain, true)
	if err != nil || result.Due != 1 || result.Resolved != 1 {
		t.Fatalf("a Redis that lost its data must not lose the entry kept in the file, got %+v, %v", result, err)
	}
	if depositCount(t, failingTx) != 1 {
		t.Fatal("the restarted reprocessor must record the deposit once")
	}
}

func TestProcessingABlockTwiceSendsEachDepositWebhookOnce(t *testing.T) {
	backends := newPendingBackends(t)
	f := newFailureFixture(t, backends.store)
	account := mocks.InsertAccount(t, "idempotency owner")
	if _, err := facades.Orm().Query().Exec("UPDATE wallets SET account_id = ? WHERE chain = ?", account.ID, scanTestChain); err != nil {
		t.Fatal(err)
	}
	webhookSvc := newWebhookSvc()
	publisher := depositevents.NewPublisher(webhookSvc, repositories.NewWalletRepository(nil), chainAssetDecimals{scanTestChain + "/" + scanTestChain: etherDecimals})
	f.svc.webhookSvc = webhookSvc
	f.svc.SetDepositEvents(publisher)
	mocks.InsertScopedWebhookConfig(t, "https://owner.test/hook", depositHookSecret, []string{string(types.EventDepositPending), depositConfirmedEvent}, &account.ID, nil)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		if _, err := f.svc.ScanBlock(context.Background(), scanTestChain, failingBlock); err != nil {
			t.Fatal(err)
		}
	}
	f.svc.txRepo = racingTxRepo{TransactionRepository: repositories.NewTransactionRepository(nil)}
	if recorded, err := f.svc.ScanBlock(context.Background(), scanTestChain, failingBlock); err != nil || recorded != 0 {
		t.Fatalf("an insert racing a recorded deposit must be absorbed by the unique index, got %d, %v", recorded, err)
	}
	f.svc.txRepo = repositories.NewTransactionRepository(nil)
	for pass := 0; pass < 2; pass++ {
		if err := f.svc.updateConfirmations(context.Background(), scanTestChain, f.adapter, scanHead+10); err != nil {
			t.Fatal(err)
		}
	}

	if depositCount(t, failingTx) != 1 || depositCount(t, healthyTx) != 1 {
		t.Fatal("each deposit must have exactly one row")
	}
	for _, eventType := range []string{string(types.EventDepositPending), depositConfirmedEvent} {
		count, err := facades.Orm().Query().Model(&models.WebhookEvent{}).Where("event_type = ?", eventType).Count()
		if err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("expected one %s outbox row per deposit (2), got %d", eventType, count)
		}
	}
}

func TestUniqueDepositIndexRejectsASecondRowForTheSameTransaction(t *testing.T) {
	mocks.TestDB(t)
	wallet := mocks.InsertWallet(t, scanTestChain)
	repo := repositories.NewTransactionRepository(nil)
	newDeposit := func(txType string) *models.Transaction {
		return &models.Transaction{
			ID: uuid.New(), WalletID: wallet.ID, ExternalUserID: "user", Chain: scanTestChain, TxType: txType,
			TxHash: "0xsame", LogIndex: models.ScannerDepositLogIndex, ToAddress: "to", Amount: "1", Asset: scanTestChain,
			Status: "pending", Direction: models.TxDirectionInbound, Source: models.TxSourceChain, RawPayload: "{}",
		}
	}
	if err := repo.Create(context.Background(), newDeposit(models.TxTypeDeposit)); err != nil {
		t.Fatal(err)
	}
	err := repo.Create(context.Background(), newDeposit(models.TxTypeDeposit))
	if !repositories.IsUniqueViolation(err) {
		t.Fatalf("expected a unique violation for a second deposit row, got %v", err)
	}
	if err := repo.Create(context.Background(), newDeposit(models.TxTypeWithdrawal)); err != nil {
		t.Fatalf("other transaction types share hashes with deposits and must not be blocked: %v", err)
	}
}
