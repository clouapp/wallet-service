package deposit

import (
	"context"
	"errors"
)

// ScanInvocation is what one deposit_scanner invocation did: the pending blocks it
// recovered and, when it ran one, the signature sync cycle.
type ScanInvocation struct {
	ResolvedPending int
	Synced          bool
	Sync            SignatureSyncSummary
}

// RunScanInvocation is the body of the deposit_scanner Lambda for one chain: scan new
// blocks (for a chain the signature sync reads, its confirmation step only), retry
// its due pending blocks, and, for a chain deposit_signatures lists, run one signature
// sync cycle once its interval elapsed since the last completed one
// (SyncSignaturesWhenDue): an invocation scheduled more often than the interval does
// not sync on every run.
func (s *Service) RunScanInvocation(ctx context.Context, chainID string) (ScanInvocation, error) {
	var run ScanInvocation
	scanErr := s.ScanLatestBlocks(ctx, chainID)
	resolved, pendingErr := s.ReprocessDuePending(ctx, chainID)
	run.ResolvedPending = resolved
	sync, synced, syncErr := s.SyncSignaturesWhenDue(ctx, chainID)
	run.Synced, run.Sync = synced, sync
	return run, errors.Join(scanErr, pendingErr, syncErr)
}
