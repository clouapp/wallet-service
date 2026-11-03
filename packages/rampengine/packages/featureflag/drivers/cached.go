package drivers

import (
	"context"

	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// Cached reads through a cache driver to an origin driver. The origin is
// authoritative: every write lands there first and the cache is updated only
// once it succeeds, so a failure between the two leaves a stale cache entry,
// never a lost write.
//
// Cache failures are handled asymmetrically, because the read path has a
// fallback and the write path does not. A cache that cannot answer a read costs
// a round-trip to the origin, so those failures are tolerated and reported
// through the handler. A cache that cannot be invalidated after an explicit
// write (Set, Delete, Purge, SetForAllScopes, InvalidateCache) has no fallback
// but the cache's own expiry, so those failures are returned -- and because
// every origin write
// here is idempotent, the caller's retry is the correct response.
//
// It holds no state of its own, so it is safe for concurrent use exactly when
// both of its legs are.
type Cached struct {
	cache   contracts.Driver
	origin  contracts.Driver
	onError func(error)
}

// CachedOption configures a Cached.
type CachedOption func(*Cached)

// WithCacheErrorHandler reports the cache failures the store chose to tolerate.
// Without one they are silent: this package never logs on its own.
func WithCacheErrorHandler(fn func(error)) CachedOption {
	return func(s *Cached) { s.onError = fn }
}

// NewCached returns a driver that serves reads from cache and writes
// through to origin.
func NewCached(cache, origin contracts.Driver, opts ...CachedOption) *Cached {
	s := &Cached{cache: cache, origin: origin}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// tolerate reports err and swallows it.
func (s *Cached) tolerate(err error) {
	if err != nil && s.onError != nil {
		s.onError(err)
	}
}

func (s *Cached) Get(ctx context.Context, feature, scopeKey string) (any, bool, error) {
	value, found, err := s.cache.Get(ctx, feature, scopeKey)
	if err != nil {
		s.tolerate(err)
	} else if found {
		return value, true, nil
	}

	value, found, err = s.origin.Get(ctx, feature, scopeKey)
	if err != nil || !found {
		return nil, false, err
	}
	// Best-effort: the value being returned is already correct, so a failure
	// here only costs the next read another trip to the origin.
	s.tolerate(s.cache.Set(ctx, feature, scopeKey, value))
	return value, true, nil
}

func (s *Cached) Set(ctx context.Context, feature, scopeKey string, value any) error {
	if err := s.origin.Set(ctx, feature, scopeKey, value); err != nil {
		return err
	}
	return s.cache.Set(ctx, feature, scopeKey, value)
}

func (s *Cached) SetIfAbsent(ctx context.Context, feature, scopeKey string, value any) (bool, error) {
	stored, err := s.origin.SetIfAbsent(ctx, feature, scopeKey, value)
	if err != nil {
		return false, err
	}
	// This is the manager's memoization path, reached from Get. Failing it on a
	// cache error would turn a cache outage into a read outage.
	if stored {
		s.tolerate(s.cache.Set(ctx, feature, scopeKey, value))
		return true, nil
	}
	// The origin refused our value, so someone else's is authoritative. Evict
	// rather than write ours: the manager re-reads next, and must see theirs.
	s.tolerate(s.cache.Delete(ctx, feature, scopeKey))
	return false, nil
}

func (s *Cached) SetForAllScopes(ctx context.Context, feature string, value any) error {
	if err := s.origin.SetForAllScopes(ctx, feature, value); err != nil {
		return err
	}
	// The cache holds only the scopes that were read, so it cannot mirror a
	// rewrite it cannot enumerate. Dropping the feature is the only correct
	// move; the entries repopulate on the next read.
	return s.cache.Purge(ctx, []string{feature})
}

func (s *Cached) Delete(ctx context.Context, feature, scopeKey string) error {
	if err := s.origin.Delete(ctx, feature, scopeKey); err != nil {
		return err
	}
	return s.cache.Delete(ctx, feature, scopeKey)
}

func (s *Cached) Purge(ctx context.Context, features []string) error {
	if err := s.origin.Purge(ctx, features); err != nil {
		return err
	}
	return s.cache.Purge(ctx, features)
}

// InvalidateCache drops every cached entry and leaves the origin alone.
//
// It is the operator's escape hatch for a cache that went stale on its own: an
// invalidation that failed after an origin write (which this store returns an
// error for, but a caller may have given up retrying), or a value written
// straight into the database by a migration or by hand. Purge is the
// destructive neighbour — it drops the values themselves — and the two must
// never be reachable from the same button.
func (s *Cached) InvalidateCache(ctx context.Context) error {
	return s.cache.Purge(ctx, nil)
}

var _ contracts.Driver = (*Cached)(nil)
var _ contracts.CacheInvalidator = (*Cached)(nil)
