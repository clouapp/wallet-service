package deposit

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	// scanActivityCacheFor is how long the health check reuses its reading of the
	// scan activity.
	scanActivityCacheFor = 15 * time.Second
	// scanActivityWriteTimeout bounds the record a scan cycle writes.
	scanActivityWriteTimeout = 2 * time.Second
	// scanActivityNotRecordedMessage is the warning of a cycle whose record failed.
	scanActivityNotRecordedMessage = "deposit scan activity not recorded"
)

// ScanRecord is one completed scan cycle of a chain: the head it read, the
// checkpoint it left and when it ended.
type ScanRecord struct {
	Head       uint64
	Checkpoint uint64
	At         time.Time
}

// LagBlocks is how many blocks the checkpoint trailed the head by; a checkpoint
// ahead of a lagging node's head trails by none.
func (r ScanRecord) LagBlocks() uint64 {
	if r.Checkpoint >= r.Head {
		return 0
	}
	return r.Head - r.Checkpoint
}

// SignatureSyncRecord is one completed signature sync cycle of a chain: how many
// accounts it read, how many of them failed, how many are still read back to their
// floor (backfilling) or were left behind by the cycle's time budget, the registered
// mints the node does not hold as token mints, the head it confirmed at, and when it
// ended. CoveredHead is the slot every account was read up to by the last cycle that
// read them all (carried from cycle to cycle for the health check's lag, 0 until one
// did); ReadEveryAccount says this cycle is that cycle. Only then are the deposits to
// every watched address up to CoveredHead known recorded, which makes it the slot the
// block scan may resume after: a carried one predates the accounts listed since.
// BackfillingSince is when the first of the cycles in a row that left an account
// backfilling ended, carried from cycle to cycle like CoveredHead (zero while none
// is), so the tracker can tell an account that never settles from a long first read.
type SignatureSyncRecord struct {
	Accounts            int
	FailedAccounts      int
	BackfillingAccounts int
	SkippedMints        []string
	Head                uint64
	CoveredHead         uint64
	ReadEveryAccount    bool
	BackfillingSince    time.Time
	At                  time.Time
}

// LagBlocks is how far the head the last cycle confirmed at is past the slot every
// account was last read up to; 0 while either is unknown. It grows only while cycles
// leave accounts unread.
func (r SignatureSyncRecord) LagBlocks() uint64 {
	if r.CoveredHead == 0 || r.Head <= r.CoveredHead {
		return 0
	}
	return r.Head - r.CoveredHead
}

// The detection modes of a chain the scan records (ScanModeRecord).
const (
	ScanModeBlocks     = "blocks"
	ScanModeSignatures = "signatures"
)

// ScanModeRecord is how the scan of a chain reads its deposits now: ScanModeBlocks,
// or ScanModeSignatures while deposit_signatures lists it (its block scan pauses).
// The scan writes it on every cycle it starts, whether or not the cycle completes;
// Since is when that mode began (as far as the writer saw), At when it was written.
type ScanModeRecord struct {
	Mode  string
	Since time.Time
	At    time.Time
}

// ChainActivity is what is known of a chain's deposit detection: its last
// completed scan cycle (nil when none was recorded), its last completed signature
// sync cycle (nil when none was), its detection mode as its scan last recorded it
// (nil when none did) and when an inbound provider webhook for it was last received
// (zero when none was).
type ChainActivity struct {
	Scan       *ScanRecord
	Signatures *SignatureSyncRecord
	Mode       *ScanModeRecord
	PushedAt   time.Time
}

// SignatureMode reports whether the chain's scan last recorded that the signature
// sync reads it: a one-off sync of a chain the block scan reads, or a sync record left
// from before the chain left deposit_signatures, never does.
func (a ChainActivity) SignatureMode() bool {
	return a.Mode != nil && a.Mode.Mode == ScanModeSignatures
}

// ScanActivityStore keeps, per chain, the last completed scan cycle (written by
// the scanner, at scan time), the last completed signature sync cycle (written by
// the sync), the detection mode (written by the scan on each cycle it starts) and the
// last provider push (written by the ingest), so the health check and the tracker
// read them without asking the chain. It holds counts, heights, mint addresses and
// times only. ReadActivity leaves out a chain with none of them.
type ScanActivityStore interface {
	RecordScan(ctx context.Context, chainID string, scan ScanRecord) error
	RecordSignatureSync(ctx context.Context, chainID string, sync SignatureSyncRecord) error
	RecordMode(ctx context.Context, chainID string, mode ScanModeRecord) error
	ReadActivity(ctx context.Context, chainIDs []string) (map[string]ChainActivity, error)
}

// ScanActivityHealth is the scan activity the health check reports, per chain a
// scanner or a provider push touched. Err is set when the store could not be read
// and there is no earlier reading to serve.
type ScanActivityHealth struct {
	Chains map[string]ChainActivity
	Err    error
}

var errScanActivityBusy = errors.New("deposit scan activity is being read")

// SetScanActivityStore wires where scan cycles are recorded and read back; without
// one nothing is recorded and the health check reports no chain.
func (s *Service) SetScanActivityStore(store ScanActivityStore) {
	s.activity = store
}

// recordScan notes a completed cycle of chainID. A record that fails is logged and
// never fails the scan: the cycle's work is already saved.
func (s *Service) recordScan(ctx context.Context, chainID string, head, checkpoint uint64) {
	if s.activity == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(ctx, scanActivityWriteTimeout)
	defer cancel()
	scan := ScanRecord{Head: head, Checkpoint: checkpoint, At: s.now().UTC()}
	if err := s.activity.RecordScan(recordCtx, chainID, scan); err != nil {
		slog.Warn(scanActivityNotRecordedMessage, "chain", chainID, "head", head, "checkpoint", checkpoint, "error", err)
	}
}

// recordSignatureSync notes a completed signature sync cycle of chainID, carrying the
// covered head of the last cycle that read every account when this one did not, and,
// while accounts stay backfilling, when the first cycle of that run ended (a previous
// record that cannot be read starts it again). A record that fails is logged and never
// fails the cycle: its work is already saved.
func (s *Service) recordSignatureSync(ctx context.Context, chainID string, summary SignatureSyncSummary) {
	if s.activity == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scanActivityWriteTimeout)
	defer cancel()
	record := SignatureSyncRecord{Accounts: summary.Accounts, FailedAccounts: summary.FailedAccounts,
		BackfillingAccounts: summary.BackfillingAccounts, SkippedMints: summary.SkippedMints,
		Head: summary.Head, CoveredHead: summary.CoveredHead, ReadEveryAccount: summary.CoveredHead != 0, At: s.now().UTC()}
	if record.CoveredHead == 0 {
		if previous, err := s.chainActivity(recordCtx, chainID); err == nil && previous.Signatures != nil {
			record.CoveredHead = previous.Signatures.CoveredHead
			if record.BackfillingAccounts > 0 && previous.Signatures.BackfillingAccounts > 0 {
				record.BackfillingSince = previous.Signatures.BackfillingSince
			}
		}
	}
	if record.BackfillingAccounts > 0 && record.BackfillingSince.IsZero() {
		record.BackfillingSince = record.At
	}
	if err := s.activity.RecordSignatureSync(recordCtx, chainID, record); err != nil {
		slog.Warn("deposit signature sync activity not recorded", "chain", chainID, "accounts", record.Accounts, "failed_accounts", record.FailedAccounts, "error", err)
	}
}

// chainActivity reads one chain's activity, bounded like the health check's reading.
func (s *Service) chainActivity(ctx context.Context, chainID string) (ChainActivity, error) {
	if s.activity == nil {
		return ChainActivity{}, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, pendingHealthTimeout)
	defer cancel()
	activity, err := s.activity.ReadActivity(readCtx, []string{chainID})
	if err != nil {
		return ChainActivity{}, err
	}
	return activity[chainID], nil
}

// modeRefreshEvery is how often an unchanged detection mode is written again, so its
// record never expires while the scan runs.
const modeRefreshEvery = time.Hour

// noteScanMode records that chainID's scan reads its deposits in mode, unless the
// record read before (previous) says so already and was written within
// modeRefreshEvery. A record that fails is logged; the cycle goes on.
func (s *Service) noteScanMode(ctx context.Context, chainID, mode string, previous *ScanModeRecord) {
	if s.activity == nil {
		return
	}
	now := s.now().UTC()
	if previous != nil && previous.Mode == mode && now.Sub(previous.At) < modeRefreshEvery {
		return
	}
	record := ScanModeRecord{Mode: mode, Since: now, At: now}
	if previous != nil && previous.Mode == mode && !previous.Since.IsZero() {
		record.Since = previous.Since
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scanActivityWriteTimeout)
	defer cancel()
	if err := s.activity.RecordMode(recordCtx, chainID, record); err != nil {
		slog.Warn("deposit scan mode not recorded", "chain", chainID, "mode", mode, "error", err)
	}
}

// ScanActivityHealth reads the scan activity of the registered chains for the
// health check, without calling any chain. The reading is reused for
// scanActivityCacheFor and read one at a time, never under a lock (healthSnapshot):
// a check that finds a reading in flight, or a reading that fails, gets the last one.
func (s *Service) ScanActivityHealth(ctx context.Context) ScanActivityHealth {
	if s.activity == nil {
		return ScanActivityHealth{Chains: map[string]ChainActivity{}}
	}
	return s.activityCache.get(ctx, s.now(), scanActivityCacheFor, healthRead[ScanActivityHealth]{
		Read: func(ctx context.Context) (ScanActivityHealth, error) {
			chains, err := s.readScanActivity(ctx)
			return ScanActivityHealth{Chains: chains}, err
		},
		Unavailable: func(err error) ScanActivityHealth {
			return ScanActivityHealth{Chains: map[string]ChainActivity{}, Err: err}
		},
		Failure: "read deposit scan activity",
		Busy:    errScanActivityBusy,
	})
}

// readScanActivity reads the activity of every registered chain under
// pendingHealthTimeout; a store answering nil reads as no activity.
func (s *Service) readScanActivity(ctx context.Context) (map[string]ChainActivity, error) {
	readCtx, cancel := context.WithTimeout(ctx, pendingHealthTimeout)
	defer cancel()
	chains, err := s.activity.ReadActivity(readCtx, s.registry.ChainIDs())
	if err != nil {
		return nil, err
	}
	if chains == nil {
		chains = map[string]ChainActivity{}
	}
	return chains, nil
}
