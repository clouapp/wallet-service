// Package scanactivity keeps, in Redis, the last completed deposit scan cycle, the
// last completed deposit signature sync cycle, the detection mode the scan last
// recorded and the last inbound provider webhook of each chain, so the health check
// and the confirmation tracker can read how far each chain's deposit detection got
// without calling the chain. It stores counts, heights, modes, token mint addresses
// and times only, never a customer's address or an id.
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
	// SignatureKeyPrefix + chain id holds the chain's last completed signature sync
	// cycle (JSON).
	SignatureKeyPrefix = "vault:signature-sync:"
	// ModeKeyPrefix + chain id holds the detection mode the chain's scan last
	// recorded (JSON).
	ModeKeyPrefix = "vault:scan-mode:"
	// recordTTL drops the record of a chain nothing has scanned or pushed to for a
	// week, so a chain taken out of scanning leaves the health check and its alerts.
	recordTTL = 7 * 24 * time.Hour
	// AlertKeyPrefix + alert key marks a raised scan alert; the same key with
	// reminderSuffix lives one reminder period, until the alert is logged again.
	AlertKeyPrefix = "vault:scan-alert:"
	reminderSuffix = ":remind"
	// alertTTL drops a raised alert nothing has confirmed for a week.
	alertTTL = 7 * 24 * time.Hour
)

// Store records and reads the scan activity. It is safe for concurrent use; the
// last writer of a chain wins.
type Store struct {
	client redis.UniversalClient
}

var (
	_ deposit.ScanActivityStore = (*Store)(nil)
	_ deposit.ScanAlertState    = (*Store)(nil)
	_ ingest.PushRecorder       = (*Store)(nil)
)

// scanRecord is the stored JSON of one scan cycle.
type scanRecord struct {
	Head       uint64    `json:"head"`
	Checkpoint uint64    `json:"checkpoint"`
	At         time.Time `json:"at"`
}

// signatureRecord is the stored JSON of one signature sync cycle.
type signatureRecord struct {
	Accounts            int       `json:"accounts"`
	FailedAccounts      int       `json:"failed_accounts"`
	BackfillingAccounts int       `json:"backfilling_accounts"`
	SkippedMints        []string  `json:"skipped_mints,omitempty"`
	Head                uint64    `json:"head"`
	CoveredHead         uint64    `json:"covered_head"`
	ReadEveryAccount    bool      `json:"read_every_account,omitempty"`
	At                  time.Time `json:"at"`
}

// modeRecord is the stored JSON of a chain's detection mode.
type modeRecord struct {
	Mode  string    `json:"mode"`
	Since time.Time `json:"since"`
	At    time.Time `json:"at"`
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

// RecordSignatureSync writes chainID's last completed signature sync cycle with
// recordTTL.
func (s *Store) RecordSignatureSync(ctx context.Context, chainID string, sync deposit.SignatureSyncRecord) error {
	if s == nil || s.client == nil {
		return nil
	}
	payload, err := json.Marshal(signatureRecord{Accounts: sync.Accounts, FailedAccounts: sync.FailedAccounts,
		BackfillingAccounts: sync.BackfillingAccounts, SkippedMints: sync.SkippedMints,
		Head: sync.Head, CoveredHead: sync.CoveredHead, ReadEveryAccount: sync.ReadEveryAccount, At: sync.At.UTC()})
	if err != nil {
		return fmt.Errorf("encode %s signature sync activity: %w", chainID, err)
	}
	if err := s.client.Set(ctx, SignatureKeyPrefix+strings.TrimSpace(chainID), payload, recordTTL).Err(); err != nil {
		return fmt.Errorf("record %s signature sync activity: %w", chainID, err)
	}
	return nil
}

// RecordMode writes chainID's detection mode with recordTTL.
func (s *Store) RecordMode(ctx context.Context, chainID string, mode deposit.ScanModeRecord) error {
	if s == nil || s.client == nil {
		return nil
	}
	payload, err := json.Marshal(modeRecord{Mode: mode.Mode, Since: mode.Since.UTC(), At: mode.At.UTC()})
	if err != nil {
		return fmt.Errorf("encode %s scan mode: %w", chainID, err)
	}
	if err := s.client.Set(ctx, ModeKeyPrefix+strings.TrimSpace(chainID), payload, recordTTL).Err(); err != nil {
		return fmt.Errorf("record %s scan mode: %w", chainID, err)
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

// ReadActivity reads the activity of chainIDs in one MGET. A chain with no scan, no
// signature sync and no push is left out; a record that cannot be decoded is left
// out and reported in the error, which keeps the chains read.
func (s *Store) ReadActivity(ctx context.Context, chainIDs []string) (map[string]deposit.ChainActivity, error) {
	activity := make(map[string]deposit.ChainActivity, len(chainIDs))
	if s == nil || s.client == nil || len(chainIDs) == 0 {
		return activity, nil
	}
	const keysPerChain = 4
	keys := make([]string, 0, keysPerChain*len(chainIDs))
	for _, chainID := range chainIDs {
		keys = append(keys, ScanKeyPrefix+chainID, PushKeyPrefix+chainID, SignatureKeyPrefix+chainID, ModeKeyPrefix+chainID)
	}
	values, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("read scan activity: %w", err)
	}
	var failures []error
	for i, chainID := range chainIDs {
		var found deposit.ChainActivity
		scan, scanErr := decodeScan(values[keysPerChain*i])
		pushedAt, pushErr := decodePush(values[keysPerChain*i+1])
		signatures, signatureErr := decodeSignatureSync(values[keysPerChain*i+2])
		mode, modeErr := decodeMode(values[keysPerChain*i+3])
		if err := errors.Join(scanErr, pushErr, signatureErr, modeErr); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", chainID, err))
			continue
		}
		found.Scan, found.PushedAt, found.Signatures, found.Mode = scan, pushedAt, signatures, mode
		if found.Scan != nil || found.Signatures != nil || found.Mode != nil || !found.PushedAt.IsZero() {
			activity[chainID] = found
		}
	}
	if len(failures) > 0 {
		return activity, fmt.Errorf("decode scan activity: %w", errors.Join(failures...))
	}
	return activity, nil
}

// RaiseAlert marks the alert of key raised. It answers AlertRaised when it was not,
// AlertReminded when it was and its reminder (remind long) ran out, AlertQuiet
// otherwise. Two processes raising at once may both see it raised or reminded.
func (s *Store) RaiseAlert(ctx context.Context, key string, remind time.Duration) (deposit.AlertTransition, error) {
	if s == nil || s.client == nil {
		return deposit.AlertRaised, nil
	}
	raised := AlertKeyPrefix + key
	first, err := s.client.SetNX(ctx, raised, 1, alertTTL).Result()
	if err != nil {
		return deposit.AlertQuiet, fmt.Errorf("raise scan alert: %w", err)
	}
	if first {
		if setErr := s.client.Set(ctx, raised+reminderSuffix, 1, remind).Err(); setErr != nil {
			return deposit.AlertRaised, fmt.Errorf("raise scan alert: reminder: %w", setErr)
		}
		return deposit.AlertRaised, nil
	}
	due, err := s.client.SetNX(ctx, raised+reminderSuffix, 1, remind).Result()
	if err != nil {
		return deposit.AlertQuiet, fmt.Errorf("remind scan alert: %w", err)
	}
	if due {
		return deposit.AlertReminded, nil
	}
	return deposit.AlertQuiet, nil
}

// ClearAlert drops the alert of key and reports whether it was raised.
func (s *Store) ClearAlert(ctx context.Context, key string) (bool, error) {
	if s == nil || s.client == nil {
		return false, nil
	}
	raised := AlertKeyPrefix + key
	removed, err := s.client.Del(ctx, raised, raised+reminderSuffix).Result()
	if err != nil {
		return false, fmt.Errorf("clear scan alert: %w", err)
	}
	return removed > 0, nil
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

// decodeSignatureSync reads one MGET value of a signature sync key; a missing key is
// nil.
func decodeSignatureSync(value any) (*deposit.SignatureSyncRecord, error) {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return nil, nil
	}
	var stored signatureRecord
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("signature sync record: %w", err)
	}
	return &deposit.SignatureSyncRecord{Accounts: stored.Accounts, FailedAccounts: stored.FailedAccounts,
		BackfillingAccounts: stored.BackfillingAccounts, SkippedMints: stored.SkippedMints,
		Head: stored.Head, CoveredHead: stored.CoveredHead, ReadEveryAccount: stored.ReadEveryAccount, At: stored.At.UTC()}, nil
}

// decodeMode reads one MGET value of a mode key; a missing key is nil.
func decodeMode(value any) (*deposit.ScanModeRecord, error) {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return nil, nil
	}
	var stored modeRecord
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("scan mode record: %w", err)
	}
	return &deposit.ScanModeRecord{Mode: stored.Mode, Since: stored.Since.UTC(), At: stored.At.UTC()}, nil
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
