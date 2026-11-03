package contracts

import "context"

// Driver is the persistence contract for feature values, one per store in
// config/featureflag.go. Implementations store and load already-resolved
// values (JSON-encoded) and know nothing about resolvers.
type Driver interface {
	Get(ctx context.Context, feature, scopeKey string) (value any, found bool, err error)
	Set(ctx context.Context, feature, scopeKey string, value any) error
	// SetIfAbsent writes only when the key is currently absent, reporting
	// whether it stored. The manager uses it to memoize without clobbering a
	// concurrent explicit override.
	SetIfAbsent(ctx context.Context, feature, scopeKey string, value any) (stored bool, err error)
	SetForAllScopes(ctx context.Context, feature string, value any) error
	Delete(ctx context.Context, feature, scopeKey string) error
	// Purge drops all stored scopes for the named features, or every feature
	// when features is nil/empty.
	Purge(ctx context.Context, features []string) error
}

// CacheInvalidator is implemented by a Driver that keeps a cache leg in front
// of an authoritative origin. It is deliberately not part of Driver: a plain
// driver has no cache to drop, and a no-op that reports success would let an
// operator believe they cleared something they did not.
//
// A Driver that wraps another Driver must forward this interface itself to keep
// it reachable: the manager's InvalidateCache type-asserts the value it holds
// directly, so a decorator that does not implement CacheInvalidator hides the
// cache leg underneath it and the flush silently reports nothing to clear.
type CacheInvalidator interface {
	// InvalidateCache drops every cached entry without touching the origin.
	InvalidateCache(ctx context.Context) error
}
