package cacheguard_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/tests/memcache"
)

func TestAcquire_FirstCallerWinsAndTheSecondSeesItHeld(t *testing.T) {
	cache := memcache.New()

	got, err := cacheguard.Acquire(cache, "lock:a", time.Minute)
	require.NoError(t, err)
	assert.True(t, got)

	got, err = cacheguard.Acquire(cache, "lock:a", time.Minute)
	require.NoError(t, err)
	assert.False(t, got, "a held key is contention, not an error")
}

func TestAcquire_ReportsAnOutageInsteadOfContention(t *testing.T) {
	got, err := cacheguard.Acquire(memcache.Down{Cache: memcache.New()}, "lock:a", time.Minute)

	assert.False(t, got)
	require.ErrorIs(t, err, cacheguard.ErrUnavailable)
}

func TestCount_AccumulatesAndStartsAtTheFirstDelta(t *testing.T) {
	cache := memcache.New()

	total, err := cacheguard.Count(cache, "n", 5, time.Minute)
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)

	total, err = cacheguard.Count(cache, "n", 3, time.Minute)
	require.NoError(t, err)
	assert.EqualValues(t, 8, total)
}

func TestCount_ReportsAnOutage(t *testing.T) {
	_, err := cacheguard.Count(memcache.Down{Cache: memcache.New()}, "n", 1, time.Minute)

	require.Error(t, err)
	assert.True(t, errors.Is(err, memcache.ErrUnavailable))
}

func TestCount_GivesTheWindowAKeyThatLapsedBetweenAddAndIncrement(t *testing.T) {
	cache := &lapsing{Cache: memcache.New()}

	total, err := cacheguard.Count(cache, "n", 1, 40*time.Millisecond)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.True(t, cache.putAfterRecreate, "a key INCRBY recreated has no TTL; Count must set one")
}

// lapsing simulates the key expiring between Add (which loses) and Increment
// (which recreates it without an expiry).
type lapsing struct {
	*memcache.Cache
	putAfterRecreate bool
}

func (l *lapsing) Add(string, any, time.Duration) bool { return false }

func (l *lapsing) Put(key string, value any, ttl time.Duration) error {
	l.putAfterRecreate = ttl > 0
	return l.Cache.Put(key, value, ttl)
}

func TestRead_ReturnsTheCountAndZeroForAnAbsentKey(t *testing.T) {
	cache := memcache.New()

	total, err := cacheguard.Read(cache, "n")
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)

	_, err = cacheguard.Count(cache, "n", 2, time.Minute)
	require.NoError(t, err)
	total, err = cacheguard.Read(cache, "n")
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
}

func TestRead_ReportsAnOutageInsteadOfZero(t *testing.T) {
	_, err := cacheguard.Read(memcache.Down{Cache: memcache.New()}, "n")

	require.ErrorIs(t, err, memcache.ErrUnavailable)
}

func TestRead_ThenCount_GivesTheLeftoverZeroKeyItsWindow(t *testing.T) {
	cache := &lapsing{Cache: memcache.New()}
	_, err := cacheguard.Read(cache, "n")
	require.NoError(t, err)

	total, err := cacheguard.Count(cache, "n", 1, time.Minute)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.True(t, cache.putAfterRecreate)
}

func TestAcquireHold_ReleaseLeavesALockAnotherHolderTook(t *testing.T) {
	cache := memcache.New()
	hold, got, err := cacheguard.AcquireHold(cache, "lock:key", time.Minute)
	require.NoError(t, err)
	require.True(t, got)

	_, again, err := cacheguard.AcquireHold(cache, "lock:key", time.Minute)
	require.NoError(t, err)
	assert.False(t, again, "a held key is contention")

	// The hold lapsed and another holder took the key: releasing must not free it.
	require.NoError(t, cache.Put("lock:key", "another-holder", time.Minute))
	hold.Release()
	assert.Equal(t, "another-holder", cache.GetString("lock:key"))
}

func TestAcquireHold_IsRenewedWhileHeldAndFreedOnRelease(t *testing.T) {
	cache := memcache.New()
	hold, got, err := cacheguard.AcquireHold(cache, "lock:key", 150*time.Millisecond)
	require.NoError(t, err)
	require.True(t, got)

	time.Sleep(450 * time.Millisecond)
	assert.True(t, cache.Has("lock:key"), "a holder still running keeps its lock past the ttl")

	hold.Release()
	assert.False(t, cache.Has("lock:key"))
	hold.Release() // a second release is a no-op
}

func TestAcquireHold_ReportsAnOutageInsteadOfContention(t *testing.T) {
	_, got, err := cacheguard.AcquireHold(memcache.Down{Cache: memcache.New()}, "lock:key", time.Minute)

	assert.False(t, got)
	require.ErrorIs(t, err, cacheguard.ErrUnavailable)
}

func TestAcquireHold_ReportsAHoldAnotherHolderTook(t *testing.T) {
	cache := memcache.New()
	hold, got, err := cacheguard.AcquireHold(cache, "lock:key", time.Minute)
	require.NoError(t, err)
	require.True(t, got)
	assert.True(t, hold.Held())
	select {
	case <-hold.Lost():
		t.Fatal("a kept hold is not lost")
	default:
	}

	require.NoError(t, cache.Put("lock:key", "another-holder", time.Minute))

	assert.False(t, hold.Held(), "the key holds another token")
	select {
	case <-hold.Lost():
	default:
		t.Fatal("a hold found lost closes Lost")
	}
	hold.Release()
	assert.Equal(t, "another-holder", cache.GetString("lock:key"))
}

func TestAcquireHold_ARenewalThatFindsAnotherTokenClosesLost(t *testing.T) {
	cache := memcache.New()
	hold, got, err := cacheguard.AcquireHold(cache, "lock:key", 90*time.Millisecond)
	require.NoError(t, err)
	require.True(t, got)
	require.NoError(t, cache.Put("lock:key", "another-holder", time.Minute))

	select {
	case <-hold.Lost():
	case <-time.After(time.Second):
		t.Fatal("the renewal must close Lost when another holder has the key")
	}
	hold.Release()
	assert.Equal(t, "another-holder", cache.GetString("lock:key"))
}
