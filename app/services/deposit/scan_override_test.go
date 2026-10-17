package deposit

import (
	"context"
	"testing"
)

func TestScan_Options_WithChainOverride(t *testing.T) {
	base := ScanOptions{BatchBlocks: 50, CatchUpBlocks: 500, Concurrency: 8}
	cases := []struct {
		name     string
		override ChainScanOverride
		want     ScanOptions
		kept     bool
	}{
		{name: "no override keeps the global window", want: base, kept: true},
		{name: "a concurrency replaces the global one", override: ChainScanOverride{Concurrency: 1}, want: ScanOptions{BatchBlocks: 50, CatchUpBlocks: 500, Concurrency: 1}, kept: true},
		{name: "a smaller window", override: ChainScanOverride{BatchBlocks: 10, CatchUpBlocks: 30, Concurrency: 2}, want: ScanOptions{BatchBlocks: 10, CatchUpBlocks: 30, Concurrency: 2}, kept: true},
		{name: "a batch above the global catch-up raises it", override: ChainScanOverride{BatchBlocks: 600}, want: ScanOptions{BatchBlocks: 600, CatchUpBlocks: 600, Concurrency: 8}, kept: true},
		{name: "a catch-up below the batch is dropped", override: ChainScanOverride{CatchUpBlocks: 20, Concurrency: 2}, want: ScanOptions{BatchBlocks: 50, CatchUpBlocks: 500, Concurrency: 2}, kept: false},
		{name: "a concurrency above the limit is dropped", override: ChainScanOverride{Concurrency: MaxScanConcurrency + 1}, want: base, kept: false},
		{name: "a negative field is dropped", override: ChainScanOverride{BatchBlocks: -5, Concurrency: 1}, want: ScanOptions{BatchBlocks: 50, CatchUpBlocks: 500, Concurrency: 1}, kept: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, kept := base.withChainOverride(tc.override)
			if got != tc.want || kept != tc.kept {
				t.Fatalf("got %+v kept %t, want %+v kept %t", got, kept, tc.want, tc.kept)
			}
		})
	}
}

func TestScan_LatestBlocks_AChainOverrideAppliesToThatChainOnly(t *testing.T) {
	global := ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4}
	f := newScanFixture(t, 3000, global).withCheckpoint(t, 1000)
	var asked []string
	f.svc.SetChainScanOverrides(func(chainID string) ChainScanOverride {
		asked = append(asked, chainID)
		if chainID == scanTestChain {
			return ChainScanOverride{CatchUpBlocks: 30, Concurrency: 1}
		}
		return ChainScanOverride{Concurrency: 16}
	})

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1030 {
		t.Fatalf("checkpoint %d, want the chain's catch-up window of 30 blocks", got)
	}
	if calls, peak := f.adapter.calls.Load(), f.adapter.peak.Load(); calls != 30 || peak != 1 {
		t.Fatalf("calls %d peak %d, want 30 blocks fetched one at a time", calls, peak)
	}
	if len(asked) != 1 || asked[0] != scanTestChain {
		t.Fatalf("override asked for %v", asked)
	}
	if f.svc.scan != global {
		t.Fatalf("a chain override must not replace the global window, got %+v", f.svc.scan)
	}
}

func TestScan_LatestBlocks_AnInvalidChainOverrideKeepsTheGlobalValue(t *testing.T) {
	f := newScanFixture(t, 3000, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4}).withCheckpoint(t, 1000)
	f.svc.SetChainScanOverrides(func(string) ChainScanOverride {
		return ChainScanOverride{Concurrency: MaxScanConcurrency + 1}
	})

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if got := f.checkpoint(t); got != 1100 {
		t.Fatalf("checkpoint %d, want the global catch-up window", got)
	}
	if peak := f.adapter.peak.Load(); peak > 4 {
		t.Fatalf("peak %d, want the global concurrency of 4", peak)
	}
}
