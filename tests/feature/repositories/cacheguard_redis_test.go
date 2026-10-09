package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/services/cacheguard"
)

// cacheguard runs on the Goravel Redis cache in production, so its assumptions
// about that driver (Add is SETNX, Increment by 0 creates "0", the cache prefix)
// are checked against it here, on the connection appfacades.Redis returns.

func TestCacheguard_Acquire_OnTheRedisCache(t *testing.T) {
	cache := appfacades.Cache()
	key := "test:cacheguard:lock:" + uuid.NewString()
	t.Cleanup(func() { cache.Forget(key) })

	got, err := cacheguard.Acquire(cache, key, time.Minute)
	require.NoError(t, err)
	assert.True(t, got)

	got, err = cacheguard.Acquire(cache, key, time.Minute)
	require.NoError(t, err)
	assert.False(t, got, "a held lock is contention, not an outage")

	redis, err := appfacades.Redis()
	require.NoError(t, err)
	ttl, err := redis.TTL(context.Background(), ":"+key).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 50*time.Second, "the lock is the cache's key (prefix %q) with the ttl", ":")
}

func TestCacheguard_Count_OnTheRedisCache(t *testing.T) {
	cache := appfacades.Cache()
	key := "test:cacheguard:count:" + uuid.NewString()
	t.Cleanup(func() { cache.Forget(key) })

	absent, err := cacheguard.Read(cache, key)
	require.NoError(t, err)
	assert.EqualValues(t, 0, absent)

	for want := int64(1); want <= 3; want++ {
		got, err := cacheguard.Count(cache, key, 1, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	read, err := cacheguard.Read(cache, key)
	require.NoError(t, err)
	assert.EqualValues(t, 3, read)

	redis, err := appfacades.Redis()
	require.NoError(t, err)
	ttl, err := redis.TTL(context.Background(), ":"+key).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 50*time.Second, "a counter that Read created must still get its window from Count")
}
