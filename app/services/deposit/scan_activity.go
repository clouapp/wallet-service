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

// ChainActivity is what is known of a chain's deposit detection: its last
// completed scan cycle (nil when none was recorded) and when an inbound provider
// webhook for it was last received (zero when none was).
type ChainActivity struct {
	Scan     *ScanRecord
	PushedAt time.Time
}

// ScanActivityStore keeps, per chain, the last completed scan cycle (written by
// the scanner, at scan time) and the last provider push (written by the ingest),
// so the health check and the tracker read them without asking the chain. It holds
// heights and times only. ReadActivity leaves out a chain with neither.
type ScanActivityStore interface {
	RecordScan(ctx context.Context, chainID string, scan ScanRecord) error
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
