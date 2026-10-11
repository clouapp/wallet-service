package deposit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/memcache"
)

// ttlRecordingCache records the TTL each lock was taken with.
type ttlRecordingCache struct {
	*memcache.Cache
	mu   sync.Mutex
	ttls map[string]time.Duration
}

func (c *ttlRecordingCache) Add(key string, value any, ttl time.Duration) bool {
	c.mu.Lock()
	c.ttls[key] = ttl
	c.mu.Unlock()
	return c.Cache.Add(key, value, ttl)
}

func (f scanFixture) holdScanLock(t *testing.T) {
	t.Helper()
	held, err := cacheguard.Acquire(f.svc.cache, scanLockKey(scanTestChain), time.Minute)
	if err != nil || !held {
		t.Fatalf("take the scan lock as another scanner: held %v err %v", held, err)
	}
}

func TestScan_LatestBlocks_SkipsTheCycleWhileAnotherScannerHoldsTheChain(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestStart)
	f.holdScanLock(t)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatalf("a held lock skips the cycle without an error, got %v", err)
	}
	if got := f.adapter.calls.Load(); got != 0 {
		t.Fatalf("no block may be fetched while another scanner holds the chain, got %d", got)
	}
	if got := f.checkpoint(t); got != checkpointTestStart {
		t.Fatalf("the checkpoint must not move, got %d", got)
	}
	if !f.svc.cache.Has(scanLockKey(scanTestChain)) {
		t.Fatal("a skipped cycle must not release the other scanner's lock")
	}

	f.svc.cache.Forget(scanLockKey(scanTestChain))
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != checkpointTestHead {
		t.Fatalf("once the lock is free the scan runs, got checkpoint %d", got)
	}
}

func TestScan_LatestBlocks_TakesAnExpiringLockAndReleasesItWhenTheCycleEnds(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestStart)
	cache := &ttlRecordingCache{Cache: memcache.New(), ttls: map[string]time.Duration{}}
	f.svc.cache = cache

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	key := scanLockKey(scanTestChain)
	if ttl := cache.ttls[key]; ttl != scanLockTTL || ttl <= time.Minute {
		t.Fatalf("the lock must expire on its own after %s, longer than the 60 s scanner Lambda, got %s", scanLockTTL, ttl)
	}
	if cache.Has(key) {
		t.Fatal("the lock must be released when the cycle ends")
	}

	f.adapter.head = checkpointTestHead + 5
	f.adapter.failAt[checkpointTestHead+1] = errors.New("rpc call getBlock: connection reset by peer")
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err == nil {
		t.Fatal("expected the failing block to fail the cycle")
	}
	if cache.Has(key) {
		t.Fatal("the lock must be released when the cycle fails too")
	}
}

func TestScan_LatestBlocks_LockStoreDownSkipsTheCycle(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestStart)
	f.svc.cache = memcache.Down{Cache: memcache.New()}

	err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain)
	if !errors.Is(err, cacheguard.ErrUnavailable) {
		t.Fatalf("a lock store that cannot be reached must fail the cycle, got %v", err)
	}
	if got := f.adapter.calls.Load(); got != 0 {
		t.Fatalf("no block may be fetched without the lock, got %d", got)
	}
	if got := f.checkpoint(t); got != checkpointTestStart {
		t.Fatalf("the checkpoint must not move, got %d", got)
	}
}

func TestScan_LatestBlocks_WithoutALockStoreSkipsTheCycle(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestStart)
	f.svc.cache = nil

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err == nil {
		t.Fatal("a scanner without a lock store must refuse to scan")
	}
	if got := f.adapter.calls.Load(); got != 0 {
		t.Fatalf("no block may be fetched without the lock, got %d", got)
	}
}

// gatedChain holds the first block fetch until the test releases it, so the scanner
// that took the lock is still scanning while the others try.
type gatedChain struct {
	*concurrentChain
	entered chan struct{}
	release chan struct{}
	first   sync.Once
}

func (c *gatedChain) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	c.first.Do(func() {
		close(c.entered)
		<-c.release
	})
	return c.concurrentChain.ScanBlock(ctx, blockNum)
}

func TestScan_LatestBlocks_ConcurrentScannersOfOneChainScanOnce(t *testing.T) {
	const scanners = 6
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestStart)
	gated := &gatedChain{concurrentChain: f.adapter, entered: make(chan struct{}), release: make(chan struct{})}
	registry := chain.NewRegistry()
	registry.RegisterChain(gated)
	f.svc.registry = registry
	// Each scanner is its own process sharing the database, Redis and the lock store.
	services := make([]*Service, scanners)
	for i := range services {
		svc := newDepositSvc(registry, newWebhookSvc())
		svc.store = f.svc.store
		svc.cache = f.svc.cache
		svc.sleep = f.sleeps.sleep
		svc.SetDepositEvents(f.events)
		if err := svc.SetScanOptions(f.svc.scan); err != nil {
			t.Fatal(err)
		}
		services[i] = svc
	}

	start := make(chan struct{})
	results := make(chan error, scanners)
	for _, svc := range services {
		go func(svc *Service) {
			<-start
			results <- svc.ScanLatestBlocks(context.Background(), scanTestChain)
		}(svc)
	}
	close(start)
	// Every scanner but the one holding the lock returns while that one is still fetching.
	for returned := 0; returned < scanners-1; returned++ {
		select {
		case err := <-results:
			if err != nil {
				close(gated.release)
				t.Fatalf("a scanner that finds the chain locked skips without an error, got %v", err)
			}
		case <-time.After(10 * time.Second):
			close(gated.release)
			t.Fatalf("only %d of %d other scanners returned while the chain was locked", returned, scanners-1)
		}
	}
	select {
	case <-gated.entered:
	case <-time.After(10 * time.Second):
		close(gated.release)
		t.Fatal("the scanner holding the lock never fetched the window")
	}
	close(gated.release)
	select {
	case err := <-results:
		if err != nil {
			t.Fatalf("the scanner holding the lock failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the scanner holding the lock did not finish")
	}

	if got := f.adapter.calls.Load(); got != int64(checkpointTestHead-checkpointTestStart) {
		t.Fatalf("exactly one scanner may fetch the window, got %d fetches", got)
	}
	if got := strings.Join(depositHashes(t), ","); got != checkpointTestTx {
		t.Fatalf("expected the deposit recorded once, got %q", got)
	}
	if got := f.checkpoint(t); got != checkpointTestHead {
		t.Fatalf("expected the checkpoint at the head, got %d", got)
	}
}

func TestRun_Operator_DefaultScanReportsAChainLockedByAnotherScanner(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestStart)
	f.holdScanLock(t)

	out, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain})
	if !errors.Is(err, ErrScanInProgress) {
		t.Fatalf("the operator must be told the scan did not run, got %v", err)
	}
	if len(out.Info) != 0 {
		t.Fatalf("no completion may be reported, got %v", out.Info)
	}
	if got := f.adapter.calls.Load(); got != 0 {
		t.Fatalf("no block may be fetched, got %d", got)
	}
}

func TestRun_Operator_TargetedScansRunWhileAnotherScannerHoldsTheChain(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestStart)
	f.svc.SetPendingStore(newPendingBackends(t).store)
	f.holdScanLock(t)

	out, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, Block: int64(checkpointTestBlock)})
	if err != nil {
		t.Fatalf("a block scan does not take the lock: %v", err)
	}
	if got := strings.Join(depositHashes(t), ","); got != checkpointTestTx {
		t.Fatalf("expected the block's deposit, got %q (%v)", got, out.Info)
	}
	if _, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, Tx: checkpointTestTx}); err != nil {
		t.Fatalf("a transaction scan does not take the lock: %v", err)
	}
	if _, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, ListPending: true}); err != nil {
		t.Fatalf("listing the pending blocks does not take the lock: %v", err)
	}
	if _, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, RetryPending: true}); err != nil {
		t.Fatalf("retrying the pending blocks does not take the lock: %v", err)
	}
	if got := f.checkpoint(t); got != checkpointTestStart {
		t.Fatalf("targeted scans must not move the checkpoint, got %d", got)
	}
	if !f.svc.cache.Has(scanLockKey(scanTestChain)) {
		t.Fatal("targeted scans must leave the other scanner's lock in place")
	}
}
