package deposit

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// The dev incident of 2026-10-18: btc moved from Bitcoin testnet3 to testnet4 kept its
// testnet3 checkpoint, far past the testnet4 head.
const (
	testnet3Checkpoint = uint64(5158070)
	testnet4Head       = uint64(156878)
)

func TestCheck_Checkpoint_NotAheadToleratesALaggingNodeOnly(t *testing.T) {
	cases := []struct {
		name             string
		checkpoint, head uint64
		wantErr          bool
	}{
		{name: "at the head", checkpoint: 100, head: 100},
		{name: "behind the head", checkpoint: 90, head: 100},
		{name: "a few blocks ahead", checkpoint: 103, head: 100},
		{name: "ahead by the tolerance", checkpoint: 100 + checkpointAheadTolerance, head: 100},
		{name: "one block past the tolerance", checkpoint: 101 + checkpointAheadTolerance, head: 100, wantErr: true},
		{name: "another network", checkpoint: testnet3Checkpoint, head: testnet4Head, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCheckpointNotAhead("btc", tc.checkpoint, tc.head)
			if tc.wantErr != (err != nil) {
				t.Fatalf("checkpoint %d, head %d: got %v, want an error: %v", tc.checkpoint, tc.head, err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, ErrCheckpointAheadOfHead) {
				t.Fatalf("expected ErrCheckpointAheadOfHead, got %v", err)
			}
		})
	}
}

func TestScan_LatestBlocks_CheckpointAheadWithinTheToleranceWaitsQuietly(t *testing.T) {
	ahead := checkpointTestHead + checkpointAheadTolerance
	f := newCheckpointFixture(t).withCheckpoint(t, ahead)

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatalf("a checkpoint a lagging node explains must not fail the cycle: %v", err)
	}
	if got := f.adapter.calls.Load(); got != 0 {
		t.Fatalf("no block may be fetched, got %d fetches", got)
	}
	if got := f.checkpoint(t); got != ahead {
		t.Fatalf("the checkpoint must stay at %d, got %d", ahead, got)
	}
}

func TestScan_LatestBlocks_CheckpointOfAnotherNetworkFailsEveryCycleAndMovesNothing(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, testnet3Checkpoint)
	f.adapter.head = testnet4Head

	for cycle := 1; cycle <= 2; cycle++ {
		err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain)
		if !errors.Is(err, ErrCheckpointAheadOfHead) {
			t.Fatalf("cycle %d: a checkpoint far past the head must fail the cycle, got %v", cycle, err)
		}
		for _, want := range []string{scanTestChain, strconv.FormatUint(testnet3Checkpoint, 10), strconv.FormatUint(testnet4Head, 10), strconv.FormatUint(testnet3Checkpoint-testnet4Head, 10), "--set-checkpoint"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("cycle %d: the error must name %q, got %q", cycle, want, err)
			}
		}
	}
	if got := f.adapter.calls.Load(); got != 0 {
		t.Fatalf("no block may be fetched, got %d fetches", got)
	}
	if got := f.checkpoint(t); got != testnet3Checkpoint {
		t.Fatalf("the guard must not move the checkpoint (that could skip deposits), got %d", got)
	}
	if f.svc.cache.Has(scanLockKey(scanTestChain)) {
		t.Fatal("a failed cycle must release the scan lock")
	}
}

func TestRun_Operator_DefaultScanReportsACheckpointFarPastTheHead(t *testing.T) {
	f := newCheckpointFixture(t).withCheckpoint(t, testnet3Checkpoint)
	f.adapter.head = testnet4Head

	_, err := f.svc.RunOperator(context.Background(), OperatorScan{ChainID: scanTestChain})

	if !errors.Is(err, ErrCheckpointAheadOfHead) || !strings.Contains(err.Error(), "deposit scan failed") {
		t.Fatalf("scan:deposits must fail with the guard error, got %v", err)
	}
}
