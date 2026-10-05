package pending

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// DurableStore writes every entry to Redis and to the local file, and reads the union
// of both. An operation fails only when every configured backend fails, so one backend
// being down never loses an entry; a delete one backend missed only causes a later,
// idempotent re-processing of that block.
type DurableStore struct {
	backends []namedStore
}

type namedStore struct {
	name  string
	store Store
}

// DurableStoreDeps is the Redis and file backends NewDurableStore writes through.
// A nil Redis or File is an unavailable backend; at least one must be set.
// Redis is the Store port; pass a nil interface when Redis is not configured.
type DurableStoreDeps struct {
	Redis Store
	File  *FileStore
}

// NewDurableStore accepts nil for an unavailable backend but needs at least one.
func NewDurableStore(deps DurableStoreDeps) (*DurableStore, error) {
	var backends []namedStore
	if deps.Redis != nil {
		backends = append(backends, namedStore{name: "redis", store: deps.Redis})
	}
	if deps.File != nil {
		backends = append(backends, namedStore{name: "file", store: deps.File})
	}
	if len(backends) == 0 {
		return nil, errors.New("pending store: at least one of redis or file is required")
	}
	return &DurableStore{backends: backends}, nil
}

func (s *DurableStore) Put(ctx context.Context, entry Entry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	return s.everyBackend("put", entry.Chain, func(store Store) error { return store.Put(ctx, entry) })
}

func (s *DurableStore) Delete(ctx context.Context, chain string, block uint64) error {
	return s.everyBackend("delete", chain, func(store Store) error { return store.Delete(ctx, chain, block) })
}

func (s *DurableStore) List(ctx context.Context, chain string) ([]Entry, error) {
	merged := map[uint64]Entry{}
	var failures []error
	for _, backend := range s.backends {
		entries, err := backend.store.List(ctx, chain)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", backend.name, err))
			continue
		}
		for _, entry := range entries {
			if current, ok := merged[entry.Block]; !ok || isNewer(entry, current) {
				merged[entry.Block] = entry
			}
		}
	}
	if len(failures) == len(s.backends) {
		return nil, fmt.Errorf("pending store list %s: every backend failed: %w", chain, errors.Join(failures...))
	}
	for _, failure := range failures {
		slog.Warn("pending deposit store degraded", "operation", "list", "chain", chain, "error", failure)
	}
	return liveEntries(merged), nil
}

func (s *DurableStore) everyBackend(operation, chain string, run func(Store) error) error {
	var failures []error
	for _, backend := range s.backends {
		if err := run(backend.store); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", backend.name, err))
		}
	}
	if len(failures) == len(s.backends) {
		return fmt.Errorf("pending store %s %s: every backend failed: %w", operation, chain, errors.Join(failures...))
	}
	for _, failure := range failures {
		slog.Warn("pending deposit store degraded", "operation", operation, "chain", chain, "error", failure)
	}
	return nil
}

func isNewer(candidate, current Entry) bool {
	if candidate.Attempts != current.Attempts {
		return candidate.Attempts > current.Attempts
	}
	return candidate.LastFailedAt.After(current.LastFailedAt)
}
