// Package pending keeps the deposit blocks the scanner could not record, so the
// checkpoint can move on without losing them. Entries live outside Postgres (Redis
// and an append-only local file), because the database is often what just failed.
package pending

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Error classes stored on an entry; they say what failed, never why in detail.
const (
	ClassRPC         = "rpc"
	ClassRateLimited = "rpc_rate_limited"
	ClassDatabase    = "database"
	ClassUnknown     = "unknown"
)

// MaxErrorLength bounds the stored error text so an entry stays a small record.
const MaxErrorLength = 500

var ErrInvalidEntry = errors.New("invalid pending deposit entry")

// Entry is one block (Solana slot) whose deposits are not recorded yet.
type Entry struct {
	Chain         string    `json:"chain"`
	Block         uint64    `json:"block"`
	TxHashes      []string  `json:"tx_hashes,omitempty"`
	ErrorClass    string    `json:"error_class"`
	LastError     string    `json:"last_error"`
	Attempts      int       `json:"attempts"`
	FirstFailedAt time.Time `json:"first_failed_at"`
	LastFailedAt  time.Time `json:"last_failed_at"`
	NextRetryAt   time.Time `json:"next_retry_at"`
}

func (e Entry) Validate() error {
	switch {
	case strings.TrimSpace(e.Chain) == "" || e.Chain != strings.TrimSpace(e.Chain):
		return fmt.Errorf("%w: chain %q", ErrInvalidEntry, e.Chain)
	case strings.ContainsAny(e.Chain, "/\\:"):
		return fmt.Errorf("%w: chain %q must not contain path or key separators", ErrInvalidEntry, e.Chain)
	case e.Block == 0:
		return fmt.Errorf("%w: block must be positive", ErrInvalidEntry)
	case e.Attempts < 1:
		return fmt.Errorf("%w: attempts must be at least 1, got %d", ErrInvalidEntry, e.Attempts)
	case e.FirstFailedAt.IsZero() || e.LastFailedAt.IsZero() || e.NextRetryAt.IsZero():
		return fmt.Errorf("%w: failure and retry times are required", ErrInvalidEntry)
	}
	return nil
}

// TruncateError keeps the first MaxErrorLength bytes of an error message.
func TruncateError(message string) string {
	if len(message) <= MaxErrorLength {
		return message
	}
	return message[:MaxErrorLength]
}

// Store persists pending entries keyed by chain and block; Put replaces an entry.
type Store interface {
	Put(ctx context.Context, entry Entry) error
	Delete(ctx context.Context, chain string, block uint64) error
	List(ctx context.Context, chain string) ([]Entry, error)
}

// Get returns the stored entry for a block, if any.
func Get(ctx context.Context, store Store, chain string, block uint64) (Entry, bool, error) {
	entries, err := store.List(ctx, chain)
	if err != nil {
		return Entry{}, false, err
	}
	for _, entry := range entries {
		if entry.Block == block {
			return entry, true, nil
		}
	}
	return Entry{}, false, nil
}

// Due returns the entries whose next retry time has come, earliest first.
func Due(entries []Entry, now time.Time) []Entry {
	due := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if !entry.NextRetryAt.After(now) {
			due = append(due, entry)
		}
	}
	sortByNextRetry(due)
	return due
}

func sortByNextRetry(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].NextRetryAt.Equal(entries[j].NextRetryAt) {
			return entries[i].Block < entries[j].Block
		}
		return entries[i].NextRetryAt.Before(entries[j].NextRetryAt)
	})
}

func sortByBlock(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Block < entries[j].Block })
}
