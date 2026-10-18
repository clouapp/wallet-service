package deposit

import (
	"context"
	"log/slog"
)

// ChainScanOverride replaces parts of the scan window and parallelism for one chain,
// so a chain served by a rate-limited RPC is fetched more slowly than the others. A
// positive field wins over the global value of the cycle, whether that comes from the
// deposit_scan setting or from the environment; zero keeps the global value.
type ChainScanOverride struct {
	BatchBlocks   int
	CatchUpBlocks int
	Concurrency   int
}

// chainScanOverrideSource reads one chain's override at the moment of use.
type chainScanOverrideSource func(chainID string) ChainScanOverride

// SetChainScanOverrides installs the per-chain override reader. Nil leaves every chain
// on the global window.
func (s *Service) SetChainScanOverrides(source func(chainID string) ChainScanOverride) {
	if s == nil {
		return
	}
	s.chainScan = source
}

// withChainOverride lays override over o. A field that is negative, above
// MaxScanConcurrency or that would leave the catch-up window below the batch is
// dropped, and kept is false; a batch above the catch-up window raises it.
func (o ScanOptions) withChainOverride(override ChainScanOverride) (ScanOptions, bool) {
	if override == (ChainScanOverride{}) {
		return o, true
	}
	applied := ApplyStoredScanOptions(o, override.BatchBlocks, override.CatchUpBlocks, override.Concurrency)
	kept := override.BatchBlocks >= 0 && override.CatchUpBlocks >= 0 && override.Concurrency >= 0 &&
		(override.BatchBlocks == 0 || applied.BatchBlocks == uint64(override.BatchBlocks)) &&
		(override.CatchUpBlocks == 0 || applied.CatchUpBlocks == uint64(override.CatchUpBlocks)) &&
		(override.Concurrency == 0 || applied.Concurrency == override.Concurrency)
	return applied, kept
}

// scanOptionsFor is the window of one cycle on chainID: the global window read for
// this cycle with the chain's override on top. It is local to the cycle, so chains
// scanned at the same time never see each other's values.
func (s *Service) scanOptionsFor(ctx context.Context, chainID string) ScanOptions {
	opts := s.resolveScanOptions(ctx)
	if s.chainScan == nil {
		return opts
	}
	override := s.chainScan(chainID)
	applied, kept := opts.withChainOverride(override)
	if !kept {
		slog.Warn("deposit scan override invalid in part; the global value stays for the fields dropped",
			"chain", chainID,
			"batch_blocks", override.BatchBlocks, "catch_up_blocks", override.CatchUpBlocks, "concurrency", override.Concurrency,
			"applied_batch_blocks", applied.BatchBlocks, "applied_catch_up_blocks", applied.CatchUpBlocks, "applied_concurrency", applied.Concurrency)
	}
	return applied
}
