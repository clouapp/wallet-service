package deposit

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

// testnet4Rewind is where the operator moves the stuck btc cursor: a few blocks below
// the testnet4 head, so the recent deposits are scanned again.
const testnet4Rewind = testnet4Head - 3

func (f scanFixture) cachedCheckpoint(t *testing.T) (uint64, bool) {
	t.Helper()
	block, found, err := f.svc.store.Uint64(context.Background(), checkpointCacheKey(scanTestChain))
	if err != nil {
		t.Fatal(err)
	}
	return block, found
}

func requireInfoLine(t *testing.T, out OperatorOutput, want string) {
	t.Helper()
	if !slices.ContainsFunc(out.Info, func(line string) bool { return strings.Contains(line, want) }) {
		t.Fatalf("expected a line containing %q, got %q", want, out.Info)
	}
}

func TestRun_Operator_SetCheckpointDryRunReportsBothCopiesAndWritesNothing(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, testnet3Checkpoint)
	f.adapter.head = testnet4Head

	out, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, SetCheckpoint: int64(testnet4Rewind)})

	if err != nil {
		t.Fatal(err)
	}
	requireInfoLine(t, out, "checkpoint of tscan: database 5158070, cache 5158070, head 156878")
	requireInfoLine(t, out, "dry run: would set the tscan checkpoint to 156875")
	if got := f.checkpoint(t); got != testnet3Checkpoint {
		t.Fatalf("a dry run must write nothing, got checkpoint %d", got)
	}
}

func TestRun_Operator_SetCheckpointApplyRewindsTheRunningScanner(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, testnet3Checkpoint)
	f.adapter.head = testnet4Head
	// The running scanner is stuck and knows its Redis copy matches the database.
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); !errors.Is(err, ErrCheckpointAheadOfHead) {
		t.Fatalf("expected the stuck scanner, got %v", err)
	}

	// The operator runs in another process sharing the database, Redis and the lock store.
	operator := newDepositSvc(f.svc.registry, newWebhookSvc())
	operator.store, operator.cache = f.svc.store, f.svc.cache
	out, err := operator.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, SetCheckpoint: int64(testnet4Rewind), Apply: true})

	if err != nil {
		t.Fatal(err)
	}
	requireInfoLine(t, out, "database 5158070, cache 5158070, head 156878")
	requireInfoLine(t, out, "checkpoint of tscan set to 156875 in the database and the cache")
	if got := f.checkpoint(t); got != testnet4Rewind {
		t.Fatalf("expected the database and Redis checkpoints moved back to %d, got %d", testnet4Rewind, got)
	}

	// The running scanner picks the new cursor up on its next cycle, without a restart.
	f.adapter.depositTo(testnet4Head-1, "tx-after-the-rewind", f.address)
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(depositHashes(t), ","); got != "tx-after-the-rewind" {
		t.Fatalf("the rewound scan must record the deposit after the new checkpoint, got %q", got)
	}
	if got := f.adapter.calls.Load(); got != int64(testnet4Head-testnet4Rewind) {
		t.Fatalf("expected blocks %d..%d fetched once each, got %d fetches", testnet4Rewind+1, testnet4Head, got)
	}
	if got := f.checkpoint(t); got != testnet4Head {
		t.Fatalf("expected the checkpoint at the head, got %d", got)
	}
}

func TestRun_Operator_SetCheckpointRefusesABlockPastTheHead(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestStart)

	out, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, SetCheckpoint: int64(checkpointTestHead + 1), Apply: true})

	if !errors.Is(err, ErrCheckpointAheadOfHead) {
		t.Fatalf("a block past the head must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "block 1009 is past the tscan head 1008") {
		t.Fatalf("the refusal must name the block and the head, got %q", err)
	}
	requireInfoLine(t, out, "database 1000, cache 1000, head 1008")
	if got := f.checkpoint(t); got != checkpointTestStart {
		t.Fatalf("a refused move must write nothing, got checkpoint %d", got)
	}
	if f.svc.cache.Has(scanLockKey(scanTestChain)) {
		t.Fatal("a refused move must release the scan lock")
	}
}

func TestRun_Operator_SetCheckpointApplyWaitsForTheScanHoldingTheChain(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, checkpointTestHead)
	f.holdScanLock(t)

	_, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, SetCheckpoint: int64(checkpointTestStart), Apply: true})
	if !errors.Is(err, ErrScanInProgress) {
		t.Fatalf("a scan in progress must stop the write, got %v", err)
	}
	if got := f.checkpoint(t); got != checkpointTestHead {
		t.Fatalf("nothing may be written while a scan holds the chain, got checkpoint %d", got)
	}

	// The dry run takes no lock: it writes nothing.
	if _, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, SetCheckpoint: int64(checkpointTestStart)}); err != nil {
		t.Fatalf("a dry run must not wait for the lock: %v", err)
	}
}

func TestRun_Operator_SetCheckpointOfAChainNeverScannedCreatesIt(t *testing.T) {
	f := newCheckpointFixture(t).withRedisCache(t)

	out, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, SetCheckpoint: int64(checkpointTestStart), Apply: true})

	if err != nil {
		t.Fatal(err)
	}
	requireInfoLine(t, out, "database none, cache none, head 1008")
	if got := f.checkpoint(t); got != checkpointTestStart {
		t.Fatalf("expected the checkpoint created at %d, got %d", checkpointTestStart, got)
	}
}

func TestRun_Operator_SetCheckpointWithoutRedisWritesTheDatabase(t *testing.T) {
	f := newCheckpointFixture(t)
	f.storeDatabaseCheckpoint(t, checkpointTestHead)

	out, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain, SetCheckpoint: int64(checkpointTestStart), Apply: true})

	if err != nil {
		t.Fatal(err)
	}
	requireInfoLine(t, out, "database 1008, cache not configured, head 1008")
	requireInfoLine(t, out, "set to 1000 in the database; the cache holds none")
	if got, _ := f.databaseCheckpoint(t); got != checkpointTestStart {
		t.Fatalf("expected the database checkpoint moved back to %d, got %d", checkpointTestStart, got)
	}
}

func TestRun_Operator_SetCheckpointRefusesBadCombinations(t *testing.T) {
	cases := map[string]OperatorScan{
		"negative block":           {ChainID: "btc", SetCheckpoint: -1},
		"with --block":             {ChainID: "btc", SetCheckpoint: 10, Block: 10},
		"with --pending":           {ChainID: "btc", SetCheckpoint: 10, ListPending: true},
		"--apply without the move": {ChainID: "btc", Apply: true},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := (&Service{}).RunOperator(context.Background(), req); err == nil {
				t.Fatal("expected the invocation to be refused")
			}
		})
	}
}

func TestDrop_Checkpoint_RemovesBothCopiesSoTheNextScanStartsAtTheHead(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, testnet3Checkpoint)
	f.adapter.head = testnet4Head

	block, found, err := f.svc.Checkpoint(context.Background(), scanTestChain)
	if err != nil || !found || block != testnet3Checkpoint {
		t.Fatalf("Checkpoint = %d, %v, %v; want the stored %d", block, found, err, testnet3Checkpoint)
	}
	block, found, err = f.svc.DropCheckpoint(context.Background(), scanTestChain)
	if err != nil || !found || block != testnet3Checkpoint {
		t.Fatalf("DropCheckpoint = %d, %v, %v; want the dropped %d", block, found, err, testnet3Checkpoint)
	}
	if _, found := f.databaseCheckpoint(t); found {
		t.Fatal("the database row must be gone")
	}
	if _, found := f.cachedCheckpoint(t); found {
		t.Fatal("the Redis copy must be gone")
	}

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != testnet4Head {
		t.Fatalf("the next scan must start at the new network's head, got %d", got)
	}
	if _, found, err := f.svc.DropCheckpoint(context.Background(), scanTestChain); err != nil || !found {
		t.Fatalf("dropping again must succeed and report the head checkpoint, got found %v err %v", found, err)
	}
	if _, found, err := f.svc.DropCheckpoint(context.Background(), scanTestChain); err != nil || found {
		t.Fatalf("dropping a chain without a checkpoint is not an error, got found %v err %v", found, err)
	}
}

func TestDrop_Checkpoint_RefusesWhileAScanHoldsTheChain(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, testnet3Checkpoint)
	f.holdScanLock(t)

	if _, _, err := f.svc.DropCheckpoint(context.Background(), scanTestChain); !errors.Is(err, ErrScanInProgress) {
		t.Fatalf("expected ErrScanInProgress, got %v", err)
	}
	if got := f.checkpoint(t); got != testnet3Checkpoint {
		t.Fatalf("nothing may be dropped while a scan holds the chain, got %d", got)
	}
}

func TestDrop_Checkpoint_KeepsTheDatabaseWhenRedisCannotBeCleared(t *testing.T) {
	f := newCheckpointFixture(t)
	f.storeDatabaseCheckpoint(t, testnet3Checkpoint)
	f.svc.store = redisStore{client: unreachableRedisClient(t)}

	if _, _, err := f.svc.DropCheckpoint(context.Background(), scanTestChain); err == nil {
		t.Fatal("a Redis that cannot be cleared must stop the drop")
	}
	if got, found := f.databaseCheckpoint(t); !found || got != testnet3Checkpoint {
		t.Fatalf("the database row must be kept, got %d (found %v)", got, found)
	}
}

func TestCheckpoint_Of_AChainOnlyRedisHoldsIsTheCachedCopy(t *testing.T) {
	f := newCheckpointFixture(t).withRedisCache(t)
	if err := f.svc.store.Set(context.Background(), checkpointCacheKey(scanTestChain), checkpointTestStart, 0); err != nil {
		t.Fatal(err)
	}

	block, found, err := f.svc.Checkpoint(context.Background(), scanTestChain)

	if err != nil || !found || block != checkpointTestStart {
		t.Fatalf("Checkpoint = %d, %v, %v; want the cached %d", block, found, err, checkpointTestStart)
	}
	if _, err := f.svc.checkpoints.Find(context.Background(), scanTestChain); !errors.Is(err, models.ErrRepositoryNotFound) {
		t.Fatalf("reading must not copy the cache to the database, got %v", err)
	}
}
