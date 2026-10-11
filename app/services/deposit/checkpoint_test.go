package deposit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// checkpointTestHead is the chain head of these tests; the deposit sits inside the
// window a resumed scan covers and outside the one a jump to the head would.
const (
	checkpointTestStart = uint64(1000)
	checkpointTestHead  = uint64(1008)
	checkpointTestBlock = uint64(1003)
	checkpointTestTx    = "tx-inside-the-window"
)

// unreadableCheckpoints is a database that refuses every checkpoint query and
// records the advances it was asked for.
type unreadableCheckpoints struct {
	err      error
	advanced []uint64
}

func (u *unreadableCheckpoints) Find(context.Context, string) (uint64, error) {
	return 0, u.err
}

func (u *unreadableCheckpoints) Advance(_ context.Context, _ string, block uint64) (uint64, error) {
	u.advanced = append(u.advanced, block)
	return 0, u.err
}

func newCheckpointFixture(t *testing.T) scanFixture {
	t.Helper()
	f := newScanFixture(t, checkpointTestHead, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4})
	f.adapter.depositTo(checkpointTestBlock, checkpointTestTx, f.address)
	return f
}

func (f scanFixture) storeDatabaseCheckpoint(t *testing.T, block uint64) {
	t.Helper()
	if _, err := f.svc.checkpoints.Advance(context.Background(), scanTestChain, block); err != nil {
		t.Fatal(err)
	}
}

func (f scanFixture) databaseCheckpoint(t *testing.T) (uint64, bool) {
	t.Helper()
	block, err := f.svc.checkpoints.Find(context.Background(), scanTestChain)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		return 0, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return block, true
}

func TestScan_LatestBlocks_RedisMissResumesFromTheDatabaseCheckpoint(t *testing.T) {
	f := newCheckpointFixture(t).withRedisCache(t)
	f.storeDatabaseCheckpoint(t, checkpointTestStart)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(depositHashes(t), ","); got != checkpointTestTx {
		t.Fatalf("the scan must resume after the database checkpoint and record the deposit, got %q", got)
	}
	if got := f.adapter.calls.Load(); got != int64(checkpointTestHead-checkpointTestStart) {
		t.Fatalf("expected blocks %d..%d fetched once each, got %d fetches", checkpointTestStart+1, checkpointTestHead, got)
	}
	if got := f.checkpoint(t); got != checkpointTestHead {
		t.Fatalf("expected the database and Redis checkpoints at the head, got %d", got)
	}
}

func TestScan_LatestBlocks_RedisErrorResumesFromTheDatabaseInsteadOfTheHead(t *testing.T) {
	f := newCheckpointFixture(t)
	f.svc.store = redisStore{client: unreachableRedisClient(t)}
	f.storeDatabaseCheckpoint(t, checkpointTestStart)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatalf("an unreachable cache must not stop the scan when the database answers: %v", err)
	}
	if got := strings.Join(depositHashes(t), ","); got != checkpointTestTx {
		t.Fatalf("a Redis error must not jump the scan to the head, got deposits %q", got)
	}
	if got, _ := f.databaseCheckpoint(t); got != checkpointTestHead {
		t.Fatalf("expected the database checkpoint at the head after the scan, got %d", got)
	}
}

// unreadableCheckpointCache is the test Redis with every checkpoint read failing, as
// a transient GET error does, while writes still succeed.
type unreadableCheckpointCache struct {
	redisStore
}

func (unreadableCheckpointCache) Uint64(context.Context, string) (uint64, bool, error) {
	return 0, false, errors.New("read tcp 127.0.0.1:6379: i/o timeout")
}

func TestScan_LatestBlocks_RedisErrorWithNoDatabaseCheckpointSkipsTheCycle(t *testing.T) {
	f := newCheckpointFixture(t).withRedisCache(t)
	f.svc.store = unreadableCheckpointCache{redisStore: f.svc.store.(redisStore)}

	err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain)
	if err == nil {
		t.Fatal("a cache that failed cannot tell a new chain from a lost key; the cycle must fail")
	}
	if got := f.adapter.calls.Load(); got != 0 {
		t.Fatalf("no block may be fetched, got %d fetches", got)
	}
	if block, found := f.databaseCheckpoint(t); found {
		t.Fatalf("the checkpoint must not be started at the head, found %d", block)
	}
	cached, found, err := f.svc.store.(unreadableCheckpointCache).redisStore.Uint64(context.Background(), checkpointCacheKey(scanTestChain))
	if err != nil || found {
		t.Fatalf("the cache must not be set to the head, got %d (found %v, err %v)", cached, found, err)
	}
}

func TestScan_LatestBlocks_DatabaseFailureSkipsTheCycle(t *testing.T) {
	f := newCheckpointFixture(t).withRedisCache(t)
	unreadable := &unreadableCheckpoints{err: errors.New("dial tcp 127.0.0.1:5433: connect: connection refused")}
	f.svc.checkpoints = unreadable

	err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected the database error to fail the cycle, got %v", err)
	}
	if got := f.adapter.calls.Load(); got != 0 {
		t.Fatalf("no block may be fetched, got %d fetches", got)
	}
	if len(unreadable.advanced) != 0 {
		t.Fatalf("nothing may be written, got advances %v", unreadable.advanced)
	}
	cached, found, err := f.svc.store.Uint64(context.Background(), checkpointCacheKey(scanTestChain))
	if err != nil || found {
		t.Fatalf("the cache must stay empty, got %d (found %v, err %v)", cached, found, err)
	}
}

func TestScan_LatestBlocks_BrandNewChainStartsAtTheHead(t *testing.T) {
	f := newCheckpointFixture(t).withRedisCache(t)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != checkpointTestHead {
		t.Fatalf("a chain neither store knows starts at the head, got %d", got)
	}
	if got := f.adapter.calls.Load(); got != 0 {
		t.Fatalf("the first scan fetches no block, got %d fetches", got)
	}
}

func TestScan_LatestBlocks_BrandNewChainWithoutRedisStartsAtTheHead(t *testing.T) {
	f := newCheckpointFixture(t)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got, found := f.databaseCheckpoint(t); !found || got != checkpointTestHead {
		t.Fatalf("expected the database checkpoint at the head, got %d (found %v)", got, found)
	}

	// The next cycle reads the database: without Redis it no longer takes the first-scan path.
	f.adapter.head = checkpointTestHead + 2
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.adapter.calls.Load(); got != 2 {
		t.Fatalf("expected the two new blocks scanned, got %d fetches", got)
	}
	if got, _ := f.databaseCheckpoint(t); got != checkpointTestHead+2 {
		t.Fatalf("expected the checkpoint to advance to %d, got %d", checkpointTestHead+2, got)
	}
}

func TestScan_LatestBlocks_CopiesACheckpointOnlyInRedisToTheDatabase(t *testing.T) {
	f := newCheckpointFixture(t).withRedisCache(t)
	f.adapter.head = checkpointTestStart
	if err := f.svc.store.Set(context.Background(), checkpointCacheKey(scanTestChain), checkpointTestStart, 0); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got, found := f.databaseCheckpoint(t); !found || got != checkpointTestStart {
		t.Fatalf("the cached checkpoint must be copied to the database, got %d (found %v)", got, found)
	}

	// Redis loses the key; a later scan resumes from the copy, not from the head.
	if err := testRedis(t).Del(context.Background(), checkpointCacheKey(scanTestChain)).Err(); err != nil {
		t.Fatal(err)
	}
	restarted := newDepositSvc(f.svc.registry, newWebhookSvc())
	restarted.store = f.svc.store
	restarted.SetDepositEvents(f.events)
	f.adapter.head = checkpointTestHead
	if err := restarted.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(depositHashes(t), ","); got != checkpointTestTx {
		t.Fatalf("a lost Redis key must not skip blocks, got deposits %q", got)
	}
	if got := f.checkpoint(t); got != checkpointTestHead {
		t.Fatalf("expected the checkpoint at the head, got %d", got)
	}
}

// overtakenChain is a chain on which another scanner moves the database checkpoint
// ahead while this one is still fetching its first block.
type overtakenChain struct {
	*concurrentChain
	overtake func()
	once     sync.Once
}

func (c *overtakenChain) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	c.once.Do(c.overtake)
	return c.concurrentChain.ScanBlock(ctx, blockNum)
}

func TestScan_LatestBlocks_SlowerScannerDoesNotRewindTheCheckpoint(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestStart)
	ahead := checkpointTestHead + 50
	overtaken := &overtakenChain{concurrentChain: f.adapter, overtake: func() { f.storeDatabaseCheckpoint(t, ahead) }}
	registry := chain.NewRegistry()
	registry.RegisterChain(overtaken)
	f.svc.registry = registry

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != ahead {
		t.Fatalf("the slower scanner must leave the database at %d and cache it, got %d", ahead, got)
	}
}
