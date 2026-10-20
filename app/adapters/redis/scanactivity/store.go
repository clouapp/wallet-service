// Package scanactivity keeps, in Redis, the last completed deposit scan cycle and
// the last inbound provider webhook of each chain, so the health check and the
// confirmation tracker can read how far each chain's deposit detection got without
// calling the chain. It stores heights and times only, never an address or an id.
package scanactivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/ingest"
)

const (
	// ScanKeyPrefix + chain id holds the chain's last completed scan cycle (JSON).
	ScanKeyPrefix = "vault:scan-activity:"
	// PushKeyPrefix + chain id holds when a provider webhook for the chain was last
	// received (RFC 3339, UTC).
	PushKeyPrefix = "vault:ingest-push:"
	// recordTTL drops the record of a chain nothing has scanned or pushed to for a
	// week, so a chain taken out of scanning leaves the health check and its alerts.
	recordTTL = 7 * 24 * time.Hour
)

// Store records and reads the scan activity. It is safe for concurrent use; the
// last writer of a chain wins.
type Store struct {
	client redis.UniversalClient
}

var (
	_ deposit.ScanActivityStore = (*Store)(nil)
	_ ingest.PushRecorder       = (*Store)(nil)
)

// scanRecord is the stored JSON of one scan cycle.
type scanRecord struct {
	Head       uint64    `json:"head"`
	Checkpoint uint64    `json:"checkpoint"`
	At         time.Time `json:"at"`
}

// New wraps client. A nil client returns a nil store, which records and reads
// nothing.
func New(client redis.UniversalClient) *Store {
	if client == nil {
		return nil
	}
	return &Store{client: client}
}

// RecordScan writes chainID's last completed scan cycle with recordTTL.
func (s *Store) RecordScan(ctx context.Context, chainID string, scan deposit.ScanRecord) error {
	if s == nil || s.client == nil {
		return nil
	}
	payload, err := json.Marshal(scanRecord{Head: scan.Head, Checkpoint: scan.Checkpoint, At: scan.At.UTC()})
	if err != nil {
		return fmt.Errorf("encode %s scan activity: %w", chainID, err)
	}
	if err := s.client.Set(ctx, ScanKeyPrefix+strings.TrimSpace(chainID), payload, recordTTL).Err(); err != nil {
		return fmt.Errorf("record %s scan activity: %w", chainID, err)
	}
	return nil
}

// RecordPush writes when a provider webhook for chainID was received, with recordTTL.
func (s *Store) RecordPush(ctx context.Context, chainID string, at time.Time) error {
	if s == nil || s.client == nil {
		return nil
	}
	if err := s.client.Set(ctx, PushKeyPrefix+strings.TrimSpace(chainID), at.UTC().Format(time.RFC3339Nano), recordTTL).Err(); err != nil {
		return fmt.Errorf("record %s provider push: %w", chainID, err)
	}
	return nil
}

// ReadActivity reads the activity of chainIDs in one MGET. A chain with neither a
// scan nor a push is left out; a record that cannot be decoded is left out and
// reported in the error, which keeps the chains read.
func (s *Store) ReadActivity(ctx context.Context, chainIDs []string) (map[string]deposit.ChainActivity, error) {
	activity := make(map[string]deposit.ChainActivity, len(chainIDs))
	if s == nil || s.client == nil || len(chainIDs) == 0 {
		return activity, nil
	}
	keys := make([]string, 0, 2*len(chainIDs))
	for _, chainID := range chainIDs {
		keys = append(keys, ScanKeyPrefix+chainID, PushKeyPrefix+chainID)
	}
	values, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("read scan activity: %w", err)
	}
	var failures []error
	for i, chainID := range chainIDs {
		var found deposit.ChainActivity
		scan, scanErr := decodeScan(values[2*i])
		pushedAt, pushErr := decodePush(values[2*i+1])
		if err := errors.Join(scanErr, pushErr); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", chainID, err))
			continue
		}
		found.Scan, found.PushedAt = scan, pushedAt
		if found.Scan != nil || !found.PushedAt.IsZero() {
			activity[chainID] = found
		}
	}
	if len(failures) > 0 {
		return activity, fmt.Errorf("decode scan activity: %w", errors.Join(failures...))
	}
	return activity, nil
}

// decodeScan reads one MGET value of a scan key; a missing key is nil.
func decodeScan(value any) (*deposit.ScanRecord, error) {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return nil, nil
	}
	var stored scanRecord
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("scan record: %w", err)
	}
	return &deposit.ScanRecord{Head: stored.Head, Checkpoint: stored.Checkpoint, At: stored.At.UTC()}, nil
}

// decodePush reads one MGET value of a push key; a missing key is the zero time.
func decodePush(value any) (time.Time, error) {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("push time: %w", err)
	}
	return at.UTC(), nil
}
