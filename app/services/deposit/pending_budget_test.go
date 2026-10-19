package deposit

import (
	"context"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/deposit/pending"
)

// putDuePending saves count pending blocks of the scan test chain, each due one
// second after the one before it (block startBlock+100+i is due i seconds ago less).
func putDuePending(t *testing.T, store pending.Store, now time.Time, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		due := now.Add(-time.Hour).Add(time.Duration(i) * time.Second)
		entry := pending.Entry{
			Chain: scanTestChain, Block: startBlock + 100 + uint64(i), Attempts: 1,
			FirstFailedAt: due, LastFailedAt: due, NextRetryAt: due, ErrorClass: pending.ClassRPC, LastError: "provider unavailable",
		}
		if err := store.Put(context.Background(), entry); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReprocess_DuePending_RetriesABoundedBatchOldestFirst(t *testing.T) {
	backends := newPendingBackends(t)
	f := newFailureFixture(t, backends.store)
	f.txRepo.heal(t)
	putDuePending(t, backends.store, f.clock.Now(), pendingPassMaxBlocks+5)
	var scanned []uint64
	f.adapter.onScan = func(block uint64) { scanned = append(scanned, block) }

	resolved, err := f.svc.ReprocessDuePending(context.Background(), scanTestChain)
	if err != nil || resolved != pendingPassMaxBlocks {
		t.Fatalf("first pass resolved %d, %v; want a batch of %d", resolved, err, pendingPassMaxBlocks)
	}
	if scanned[0] != startBlock+100 || scanned[len(scanned)-1] != startBlock+100+pendingPassMaxBlocks-1 {
		t.Fatalf("scanned %v, want the oldest due blocks first", scanned)
	}
	if left := pendingEntries(t, backends.store); len(left) != 5 {
		t.Fatalf("%d entries left, want the 5 youngest for the next pass", len(left))
	}
	if resolved, err := f.svc.ReprocessDuePending(context.Background(), scanTestChain); err != nil || resolved != 5 {
		t.Fatalf("second pass resolved %d, %v", resolved, err)
	}
}

func TestReprocess_DuePending_StopsAtThePassTimeBudget(t *testing.T) {
	backends := newPendingBackends(t)
	f := newFailureFixture(t, backends.store)
	f.txRepo.heal(t)
	putDuePending(t, backends.store, f.clock.Now(), 6)
	// Each block takes two seconds: a fallback that answers, a primary that hangs.
	f.adapter.onScan = func(uint64) { f.clock.advance(2 * time.Second) }

	resolved, err := f.svc.ReprocessDuePending(context.Background(), scanTestChain)
	if err != nil || resolved != 3 {
		t.Fatalf("resolved %d, %v; want the pass to stop once %s are spent", resolved, err, pendingPassBudget)
	}
	if left := pendingEntries(t, backends.store); len(left) != 3 {
		t.Fatalf("%d entries left, want 3 for the next pass", len(left))
	}
	f.adapter.onScan = func(uint64) { f.clock.advance(3 * time.Second) }
	if result, err := f.svc.ReprocessPending(context.Background(), scanTestChain, true); err != nil || result.Resolved != 3 {
		t.Fatalf("an operator's forced pass has no budget: %+v, %v", result, err)
	}
}
