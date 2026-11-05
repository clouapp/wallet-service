// Package locking provides cross-request critical-section primitives backed by
// the database. Postgres advisory locks are used (rather than rows or app-level
// mutexes) because they survive across processes, replicas, and request
// boundaries — the only state shared between two API workers is the database
// itself.
package locking

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// Locker serializes execution of `fn` across all processes for the same key.
//
// Concrete implementations decide where the lock lives (Postgres, Redis, …).
// The contract: while fn runs for a given key, no other call with the same key
// in any process will be inside fn at the same time. fn's error (or panic) is
// returned to the caller; the lock is always released before WithLock returns.
type Locker interface {
	WithLock(ctx context.Context, key string, fn func(ctx context.Context) error) error
}

// AdvisoryLocker is the Postgres-backed Locker. It opens a database
// transaction, calls pg_advisory_xact_lock(<hash(key)>), runs fn while the
// lock is held, then commits — which releases the lock atomically. If fn
// returns an error the transaction rolls back, which also releases the lock.
//
// The transaction itself is just the lock holder; writes performed by fn go
// through whatever connection the caller normally uses. Serialization is
// guaranteed by the lock alone — the inner code does not need to share this
// transaction.
type AdvisoryLocker struct{}

func NewAdvisoryLocker() *AdvisoryLocker {
	return &AdvisoryLocker{}
}

// WithLock takes a Postgres transaction-scoped advisory lock keyed by the
// FNV-64a hash of `key`, then runs fn. Hash collisions cause unrelated keys to
// serialize against each other but never cause a correctness bug — the lock is
// over-conservative under collision, not under-conservative.
func (l *AdvisoryLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error) error {
	hash := hashKey(key)
	return facades.Orm().WithContext(ctx).Transaction(func(tx orm.Query) error {
		if _, err := tx.Exec("SELECT pg_advisory_xact_lock(?)", hash); err != nil {
			return fmt.Errorf("advisory lock %q: %w", key, err)
		}
		return fn(ctx)
	})
}

// hashKey reduces an arbitrary string to a deterministic int64 suitable for
// pg_advisory_xact_lock(bigint). FNV-64a is non-cryptographic but well
// distributed; bit-cast to int64 because Postgres bigint is signed.
func hashKey(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64()) //nolint:gosec // intentional bit-cast to fit bigint
}

var _ Locker = (*AdvisoryLocker)(nil)
