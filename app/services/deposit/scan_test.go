package deposit

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/macrowallets/waas/tests/testutil"
)

// scanTestChain has its own checkpoint key, so these tests never touch a real chain's.
const scanTestChain = "tscan"

// concurrentChain serves ScanBlock from a fixed block map and is safe for the parallel
// fetches of a catch-up window, unlike MockChain's call counter.
type concurrentChain struct {
	*mocks.MockChain
	head      uint64
	transfers map[uint64][]types.DetectedTransfer
	failAt    map[uint64]error
	txBlocks  map[string]uint64

	flakyMu sync.Mutex
	// flaky fails a block's next N fetches, then serves it.
	flaky map[uint64]int

	calls    atomic.Int64
	inFlight atomic.Int64
	peak     atomic.Int64
}

func newConcurrentChain(head uint64) *concurrentChain {
	base := mocks.NewMockChain(scanTestChain)
	base.RequiredConfirmationsVal = 1
	return &concurrentChain{
		MockChain: base,
		head:      head,
		transfers: map[uint64][]types.DetectedTransfer{},
		failAt:    map[uint64]error{},
		txBlocks:  map[string]uint64{},
		flaky:     map[uint64]int{},
	}
}

func (c *concurrentChain) failNextFetches(blockNum uint64, times int) {
	c.flakyMu.Lock()
	defer c.flakyMu.Unlock()
	c.flaky[blockNum] = times
}

func (c *concurrentChain) takeFlakyFailure(blockNum uint64) bool {
	c.flakyMu.Lock()
	defer c.flakyMu.Unlock()
	if c.flaky[blockNum] == 0 {
		return false
	}
	c.flaky[blockNum]--
	return true
}

func (c *concurrentChain) GetLatestBlock(context.Context) (uint64, error) { return c.head, nil }

func (c *concurrentChain) GetTransactionBlock(_ context.Context, txHash string) (uint64, error) {
	return c.txBlocks[txHash], nil
}

func (c *concurrentChain) ScanBlock(_ context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	c.calls.Add(1)
	current := c.inFlight.Add(1)
	defer c.inFlight.Add(-1)
	for {
		peak := c.peak.Load()
		if current <= peak || c.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	// Later blocks answer first, so an in-order result proves the reordering.
	time.Sleep(time.Duration(blockNum%5) * time.Millisecond)
	if err := c.failAt[blockNum]; err != nil {
		return nil, err
	}
	if c.takeFlakyFailure(blockNum) {
		return nil, fmt.Errorf("rpc call getBlock %d: connection reset by peer", blockNum)
	}
	return c.transfers[blockNum], nil
}

func (c *concurrentChain) depositTo(blockNum uint64, txHash, to string) {
	c.transfers[blockNum] = append(c.transfers[blockNum], types.DetectedTransfer{
		TxHash: txHash, BlockNumber: blockNum, BlockHash: fmt.Sprintf("hash-%d", blockNum),
		From: "sender", To: to, Amount: big.NewInt(1000), Asset: scanTestChain,
	})
	c.txBlocks[txHash] = blockNum
}

type recordingDepositEvents struct {
	mu     sync.Mutex
	events []types.EventType
}

func (r *recordingDepositEvents) Publish(_ context.Context, eventType types.EventType, _ models.Transaction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, eventType)
	return nil
}

func (r *recordingDepositEvents) count(eventType types.EventType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, event := range r.events {
		if event == eventType {
			n++
		}
	}
	return n
}

type scanFixture struct {
	svc     *Service
	adapter *concurrentChain
	events  *recordingDepositEvents
	address string
	sleeps  *recordedSleeps
}

// recordedSleeps stands in for the immediate-retry waits, so tests assert the backoff
// without spending it.
type recordedSleeps struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (r *recordedSleeps) sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delays = append(r.delays, d)
	return nil
}

func (r *recordedSleeps) recorded() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.delays...)
}

func newScanFixture(t *testing.T, head uint64, opts ScanOptions) scanFixture {
	t.Helper()
	mocks.TestDB(t)
	adapter := newConcurrentChain(head)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	wallet := mocks.InsertWallet(t, scanTestChain)
	address := mocks.InsertAddress(t, wallet.ID, scanTestChain, "watched-addr", "user_scan", 0)

	svc := newDepositSvc(registry, newWebhookSvc())
	if err := svc.SetScanOptions(opts); err != nil {
		t.Fatal(err)
	}
	events := &recordingDepositEvents{}
	svc.SetDepositEvents(events)
	sleeps := &recordedSleeps{}
	svc.sleep = sleeps.sleep
	return scanFixture{svc: svc, adapter: adapter, events: events, address: address.Address, sleeps: sleeps}
}

func (f scanFixture) withRedisCheckpoint(t *testing.T, checkpoint uint64) scanFixture {
	t.Helper()
	rdb := testRedis(t)
	key := "vault:checkpoint:" + scanTestChain
	if err := rdb.Set(context.Background(), key, checkpoint, 0).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rdb.Del(context.Background(), key, addressCacheKey(scanTestChain)) })
	f.svc.store = redisStore{client: rdb}
	if err := f.svc.RefreshAddressCache(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f scanFixture) checkpoint(t *testing.T) uint64 {
	t.Helper()
	value, err := f.svc.loadCheckpoint(context.Background(), scanTestChain)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func depositHashes(t *testing.T) []string {
	t.Helper()
	var deposits []models.Transaction
	if err := facades.Orm().Query().Where("chain", scanTestChain).Where("tx_type", models.TxTypeDeposit).Order("block_number").Find(&deposits); err != nil {
		t.Fatal(err)
	}
	hashes := make([]string, len(deposits))
	for i, deposit := range deposits {
		hashes[i] = deposit.TxHash
	}
	return hashes
}

func TestScanOptionsFromSettings(t *testing.T) {
	cases := []struct {
		name                        string
		batch, catchUp, concurrency int
		want                        ScanOptions
		wantErr                     string
	}{
		{name: "zeros keep the defaults", want: DefaultScanOptions()},
		{name: "explicit values", batch: 20, catchUp: 200, concurrency: 4, want: ScanOptions{BatchBlocks: 20, CatchUpBlocks: 200, Concurrency: 4}},
		{name: "a batch above the default catch-up raises it", batch: 1000, want: ScanOptions{BatchBlocks: 1000, CatchUpBlocks: 1000, Concurrency: DefaultScanConcurrency}},
		{name: "explicit catch-up below the batch", batch: 100, catchUp: 50, wantErr: "must not be smaller"},
		{name: "negative value", concurrency: -1, wantErr: "must not be negative"},
		{name: "concurrency above the limit", concurrency: MaxScanConcurrency + 1, wantErr: "between 1 and"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ScanOptionsFromSettings(tc.batch, tc.catchUp, tc.concurrency)
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

func TestApplyStoredScanOptions(t *testing.T) {
	base := ScanOptions{BatchBlocks: 50, CatchUpBlocks: 500, Concurrency: 8}
	cases := []struct {
		name                        string
		batch, catchUp, concurrency int
		want                        ScanOptions
	}{
		{name: "zeros keep the environment window", want: base},
		{name: "one stored field overrides", batch: 80, want: ScanOptions{BatchBlocks: 80, CatchUpBlocks: 500, Concurrency: 8}},
		{name: "a larger batch raises an omitted catch-up", batch: 1000, want: ScanOptions{BatchBlocks: 1000, CatchUpBlocks: 1000, Concurrency: 8}},
		{name: "batch and catch-up apply together", batch: 1000, catchUp: 2000, concurrency: 4, want: ScanOptions{BatchBlocks: 1000, CatchUpBlocks: 2000, Concurrency: 4}},
		{name: "stored catch-up and concurrency", catchUp: 200, concurrency: 4, want: ScanOptions{BatchBlocks: 50, CatchUpBlocks: 200, Concurrency: 4}},
		{name: "an invalid catch-up is dropped and the batch still applies", batch: 100, catchUp: 50, concurrency: 4, want: ScanOptions{BatchBlocks: 100, CatchUpBlocks: 500, Concurrency: 4}},
		{name: "concurrency above the limit is dropped", concurrency: MaxScanConcurrency + 1, want: base},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyStoredScanOptions(base, tc.batch, tc.catchUp, tc.concurrency)
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveScanOptions_StoredWindowReplacesTheEnvironmentAndAFailureRestoresIt(t *testing.T) {
	svc := &Service{}
	envWindow := ScanOptions{BatchBlocks: 50, CatchUpBlocks: 500, Concurrency: 8}
	if err := svc.SetScanOptions(envWindow); err != nil {
		t.Fatal(err)
	}
	svc.SetScanOptionSource(func(context.Context) (ScanOptions, error) {
		return ScanOptions{BatchBlocks: 12, CatchUpBlocks: 120, Concurrency: 2}, nil
	})
	svc.resolveScanOptions(context.Background())
	if svc.scan.BatchBlocks != 12 || svc.scan.Concurrency != 2 {
		t.Fatalf("stored window = %+v", svc.scan)
	}

	svc.SetScanOptionSource(func(context.Context) (ScanOptions, error) {
		return ScanOptions{}, errors.New("db down")
	})
	svc.resolveScanOptions(context.Background())
	if svc.scan != envWindow {
		t.Fatalf("after a read failure = %+v, want environment %+v", svc.scan, envWindow)
	}
}

func TestScanLatestBlocks_ReadsTheWindowOnEachInvocation(t *testing.T) {
	svc := &Service{registry: chain.NewRegistry()}
	if err := svc.SetScanOptions(DefaultScanOptions()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.SetScanOptionSource(func(context.Context) (ScanOptions, error) {
		calls++
		return ScanOptions{BatchBlocks: uint64(10 + calls), CatchUpBlocks: 500, Concurrency: 8}, nil
	})

	if err := svc.ScanLatestBlocks(context.Background(), "not-a-chain"); err == nil {
		t.Fatal("expected an unknown chain")
	}
	if calls != 1 || svc.scan.BatchBlocks != 11 {
		t.Fatalf("first read calls=%d window=%+v", calls, svc.scan)
	}
	if err := svc.ScanLatestBlocks(context.Background(), "not-a-chain"); err == nil {
		t.Fatal("expected an unknown chain")
	}
	if calls != 2 || svc.scan.BatchBlocks != 12 {
		t.Fatalf("second read calls=%d window=%+v", calls, svc.scan)
	}
}

func TestScanOptionsForRun(t *testing.T) {
	env := ScanOptions{BatchBlocks: 50, CatchUpBlocks: 80, Concurrency: 8}
	cases := []struct {
		name                        string
		batch, catchUp, concurrency int
		readErr                     error
		want                        ScanOptions
		wantErr                     string
	}{
		{name: "a stored field overrides the environment", batch: 20, want: ScanOptions{BatchBlocks: 20, CatchUpBlocks: 80, Concurrency: 8}},
		{name: "zeros keep the environment window", want: env},
		{name: "a larger batch raises an omitted catch-up to the batch", batch: 100, want: ScanOptions{BatchBlocks: 100, CatchUpBlocks: 100, Concurrency: 8}},
		{name: "an invalid combination keeps the environment window", batch: 100, catchUp: 40, concurrency: 4, want: env},
		{name: "a failed read keeps the environment window", readErr: errors.New("db down"), want: env},
		{name: "concurrency above the limit keeps the environment window", concurrency: MaxScanConcurrency + 1, want: env},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ScanOptionsForRun(50, 80, 8, tc.batch, tc.catchUp, tc.concurrency, tc.readErr)
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

	_, err := ScanOptionsForRun(-1, 80, 8, 0, 0, 0, nil)
	if err == nil {
		t.Fatal("expected an invalid environment window to be rejected")
	}
}

func TestScanLatestBlocks_EachScanCallsScanOptionsFromSettings(t *testing.T) {
	const (
		envBatch       = 10
		envCatchUp     = 100
		envConcurrency = 4
		head           = uint64(5000)
		start          = uint64(1000)
	)
	f := newScanFixture(t, head, DefaultScanOptions())
	rdb := testutil.TestRedis(t)
	key := "vault:checkpoint:" + scanTestChain
	if err := rdb.Set(context.Background(), key, start, 0).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rdb.Del(context.Background(), key, addressCacheKey(scanTestChain)).Err()
	})
	f.svc.store = redisStore{client: rdb}
	if err := f.svc.RefreshAddressCache(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}

	type storedWindow struct {
		batch, catchUp, concurrency int
		readErr                     error
	}
	current := storedWindow{batch: 20, catchUp: 200, concurrency: 4}
	f.svc.SetScanOptionSource(func(context.Context) (ScanOptions, error) {
		return ScanOptionsForRun(envBatch, envCatchUp, envConcurrency, current.batch, current.catchUp, current.concurrency, current.readErr)
	})

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1200 {
		t.Fatalf("first scan checkpoint = %d, want 1200 from the stored catch-up", got)
	}

	current.catchUp = 150
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1350 {
		t.Fatalf("second scan checkpoint = %d, want 1350 after the settings change", got)
	}

	current = storedWindow{}
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1450 {
		t.Fatalf("missing row checkpoint = %d, want the environment catch-up of 100", got)
	}

	current = storedWindow{batch: 100, catchUp: 40, concurrency: 4}
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1550 {
		t.Fatalf("invalid row checkpoint = %d, want the environment window to keep the scan moving", got)
	}

	current = storedWindow{readErr: errors.New("db down")}
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1650 {
		t.Fatalf("failed read checkpoint = %d, want the environment window and a completed scan", got)
	}
}

func TestSetScanOptions_KeepsTheCurrentOptionsWhenInvalid(t *testing.T) {
	svc := newDepositSvc(chain.NewRegistry(), nil)
	if err := svc.SetScanOptions(ScanOptions{BatchBlocks: 0, CatchUpBlocks: 10, Concurrency: 1}); err == nil {
		t.Fatal("expected a zero batch to be rejected")
	}
	if svc.scan != DefaultScanOptions() {
		t.Fatalf("invalid options must not replace the current ones, got %+v", svc.scan)
	}
}

func TestScanWindow(t *testing.T) {
	svc := newDepositSvc(chain.NewRegistry(), nil)
	if err := svc.SetScanOptions(ScanOptions{BatchBlocks: 50, CatchUpBlocks: 500, Concurrency: 8}); err != nil {
		t.Fatal(err)
	}
	for lag, want := range map[uint64]uint64{1: 1, 50: 50, 51: 51, 499: 499, 500: 500, 90_000: 500} {
		if got := svc.scanWindow(lag); got != want {
			t.Errorf("lag %d: window %d, want %d", lag, got, want)
		}
	}
}

func TestScanRange_RecordsInBlockOrderWithParallelFetches(t *testing.T) {
	f := newScanFixture(t, 1000, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4})
	f.adapter.depositTo(103, "tx-103", f.address)
	f.adapter.depositTo(117, "tx-117", f.address)
	f.adapter.depositTo(117, "tx-117-other", "someone-else")
	f.adapter.depositTo(130, "tx-130", f.address)

	scanned, err := f.svc.scanRange(context.Background(), scanTestChain, f.adapter, 101, 130)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 130 {
		t.Fatalf("expected the whole range scanned, got %d", scanned)
	}
	if got := f.adapter.calls.Load(); got != 30 {
		t.Fatalf("expected each block fetched once, got %d fetches", got)
	}
	if peak := f.adapter.peak.Load(); peak < 2 || peak > 4 {
		t.Fatalf("expected parallel fetches bounded by 4, peak was %d", peak)
	}
	if got := strings.Join(depositHashes(t), ","); got != "tx-103,tx-117,tx-130" {
		t.Fatalf("unexpected deposits %s", got)
	}
	if n := f.events.count(types.EventDepositPending); n != 3 {
		t.Fatalf("expected one deposit.pending per deposit, got %d", n)
	}
}

func TestScanRange_StopsBeforeTheFirstFailedBlock(t *testing.T) {
	f := newScanFixture(t, 1000, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4})
	f.adapter.depositTo(106, "tx-106", f.address)
	f.adapter.depositTo(108, "tx-108", f.address)
	f.adapter.failAt[107] = errors.New("rpc call getBlock: rate limited (HTTP 429) after 6 attempts")

	scanned, err := f.svc.scanRange(context.Background(), scanTestChain, f.adapter, 101, 120)
	if err == nil || !strings.Contains(err.Error(), "scan block 107") {
		t.Fatalf("expected the block 107 failure, got %v", err)
	}
	if scanned != 106 {
		t.Fatalf("expected the unbroken run to end at 106, got %d", scanned)
	}
	if got := strings.Join(depositHashes(t), ","); got != "tx-106" {
		t.Fatalf("nothing after the gap may be recorded before the gap is, got %s", got)
	}
}

func TestScanRange_RejectsBlockZeroAndAcceptsAnEmptyRange(t *testing.T) {
	f := newScanFixture(t, 1000, DefaultScanOptions())
	if _, err := f.svc.scanRange(context.Background(), scanTestChain, f.adapter, 0, 5); err == nil {
		t.Fatal("expected a range starting at block 0 to be rejected")
	}
	scanned, err := f.svc.scanRange(context.Background(), scanTestChain, f.adapter, 11, 10)
	if err != nil || scanned != 10 || f.adapter.calls.Load() != 0 {
		t.Fatalf("an empty range must scan nothing, got scanned=%d err=%v calls=%d", scanned, err, f.adapter.calls.Load())
	}
}

func TestScanLatestBlocks_CatchesUpWithoutSkippingBlocks(t *testing.T) {
	f := newScanFixture(t, 3000, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4}).withRedisCheckpoint(t, 1000)
	f.adapter.depositTo(1001, "tx-first", f.address)
	f.adapter.depositTo(1100, "tx-last", f.address)
	f.adapter.depositTo(1101, "tx-next-window", f.address)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1100 {
		t.Fatalf("expected the checkpoint to advance by the catch-up window to 1100, got %d", got)
	}
	if got := strings.Join(depositHashes(t), ","); got != "tx-first,tx-last" {
		t.Fatalf("unexpected deposits after the first window: %s", got)
	}

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1200 {
		t.Fatalf("expected the next window to continue from 1100, got %d", got)
	}
	if got := strings.Join(depositHashes(t), ","); got != "tx-first,tx-last,tx-next-window" {
		t.Fatalf("unexpected deposits after the second window: %s", got)
	}
}

func TestScanLatestBlocks_NearTheHeadUsesTheNormalBatch(t *testing.T) {
	f := newScanFixture(t, 1008, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4}).withRedisCheckpoint(t, 1000)
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1008 {
		t.Fatalf("expected the checkpoint at the head, got %d", got)
	}
	if got := f.adapter.calls.Load(); got != 8 {
		t.Fatalf("expected 8 blocks fetched, got %d", got)
	}
}

func TestScanLatestBlocks_FailedBlockKeepsTheCheckpointBeforeIt(t *testing.T) {
	f := newScanFixture(t, 3000, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4}).withRedisCheckpoint(t, 1000)
	f.adapter.failAt[1050] = errors.New("rpc call getBlock: Post: context deadline exceeded")

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err == nil {
		t.Fatal("expected the failed block to fail the cycle")
	}
	if got := f.checkpoint(t); got != 1049 {
		t.Fatalf("expected the checkpoint to stop right before the failed block, got %d", got)
	}
}

func TestScanBlock_TargetedScanIsIdempotentAndLeavesTheCheckpoint(t *testing.T) {
	f := newScanFixture(t, 1000, DefaultScanOptions()).withRedisCheckpoint(t, 500)
	f.adapter.depositTo(900, "tx-internal", f.address)
	f.adapter.depositTo(900, "tx-unwatched", "someone-else")

	for pass, wantRecorded := range []int{1, 0} {
		recorded, err := f.svc.ScanBlock(context.Background(), scanTestChain, 900)
		if err != nil {
			t.Fatal(err)
		}
		if recorded != wantRecorded {
			t.Fatalf("pass %d: recorded %d, want %d", pass+1, recorded, wantRecorded)
		}
	}
	if got := strings.Join(depositHashes(t), ","); got != "tx-internal" {
		t.Fatalf("expected one deposit row, got %s", got)
	}
	if n := f.events.count(types.EventDepositPending); n != 1 {
		t.Fatalf("expected one deposit.pending, got %d", n)
	}
	if got := f.checkpoint(t); got != 500 {
		t.Fatalf("a targeted scan must not move the checkpoint, got %d", got)
	}

	// The regular scan reaching the block later records nothing new.
	f.adapter.head = 950
	if err := f.svc.SetScanOptions(ScanOptions{BatchBlocks: 500, CatchUpBlocks: 500, Concurrency: 8}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(depositHashes(t), ","); got != "tx-internal" {
		t.Fatalf("the regular scan duplicated the deposit: %s", got)
	}
	if n := f.events.count(types.EventDepositPending); n != 1 {
		t.Fatalf("the regular scan sent deposit.pending again: %d", n)
	}
}

func TestScanBlock_RefusesBlocksPastTheHead(t *testing.T) {
	f := newScanFixture(t, 1000, DefaultScanOptions())
	for _, block := range []uint64{0, 1001} {
		if _, err := f.svc.ScanBlock(context.Background(), scanTestChain, block); err == nil {
			t.Fatalf("expected block %d to be refused", block)
		}
	}
	if f.adapter.calls.Load() != 0 {
		t.Fatal("a refused block must not be fetched")
	}
	if _, err := f.svc.ScanBlock(context.Background(), "unknown-chain", 10); err == nil {
		t.Fatal("expected an unknown chain to be refused")
	}
}

func TestScanTransaction_RecordsOnlyThatTransaction(t *testing.T) {
	f := newScanFixture(t, 1000, DefaultScanOptions())
	f.adapter.depositTo(700, "0xabcdef", f.address)
	f.adapter.depositTo(700, "0x123456", f.address)

	recorded, err := f.svc.ScanTransaction(context.Background(), scanTestChain, "0xabcdef")
	if err != nil || recorded != 1 {
		t.Fatalf("expected one deposit recorded, got %d, %v", recorded, err)
	}
	if got := strings.Join(depositHashes(t), ","); got != "0xabcdef" {
		t.Fatalf("expected only the requested transaction, got %s", got)
	}
}

func TestScanTransaction_RefusesUnknownOrBlankTransactions(t *testing.T) {
	f := newScanFixture(t, 1000, DefaultScanOptions())
	if _, err := f.svc.ScanTransaction(context.Background(), scanTestChain, "  "); err == nil {
		t.Fatal("expected a blank hash to be refused")
	}
	_, err := f.svc.ScanTransaction(context.Background(), scanTestChain, "0xunknown")
	if err == nil || !strings.Contains(err.Error(), "not final yet") {
		t.Fatalf("expected an unknown transaction to be refused, got %v", err)
	}
}

func TestSameTxHash(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0xABcd", "0xabCD", true},
		{"0xabcd", "0xabce", false},
		{"5FE9YMmi", "5FE9YMmi", true},
		{"5FE9YMmi", "5fe9ymmi", false},
	}
	for _, tc := range cases {
		if got := sameTxHash(tc.a, tc.b); got != tc.want {
			t.Errorf("sameTxHash(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestScanTransaction_SolanaSignatureFromRecordedDevnetBlock(t *testing.T) {
	mocks.TestDB(t)
	adapter := solanaFixtureRPC(t)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	wallet := mocks.InsertWallet(t, models.ChainSOL)
	mocks.InsertAddress(t, wallet.ID, models.ChainSOL, solFixtureRecipient, "", 1)
	svc := NewService(Deps{
		Registry:     registry,
		Webhook:      newWebhookSvc(),
		Addresses:    repositories.NewAddressRepository(nil),
		Transactions: repositories.NewTransactionRepository(nil),
	})

	for pass, wantRecorded := range []int{1, 0} {
		recorded, err := svc.ScanTransaction(context.Background(), models.ChainSOL, solFixtureSignature)
		if err != nil {
			t.Fatal(err)
		}
		if recorded != wantRecorded {
			t.Fatalf("pass %d: recorded %d, want %d", pass+1, recorded, wantRecorded)
		}
	}
	var deposit models.Transaction
	if err := facades.Orm().Query().Where("chain", models.ChainSOL).Where("tx_type", models.TxTypeDeposit).First(&deposit); err != nil {
		t.Fatal(err)
	}
	if deposit.TxHash != solFixtureSignature || deposit.BlockNumber != solFixtureSlot || deposit.Status != "pending" {
		t.Fatalf("unexpected deposit %+v", deposit)
	}
}
