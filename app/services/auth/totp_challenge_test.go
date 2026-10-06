package auth_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
	"github.com/stretchr/testify/assert"
)

func redisChallengeStore(t *testing.T, ttl time.Duration) *authsvc.CacheTOTPChallengeStore {
	t.Helper()
	testutil.TestRedis(t)
	store, err := authsvc.NewCacheTOTPChallengeStore(authsvc.ChallengeStoreDeps{
		Cache: facades.Cache(),
		TTL:   ttl,
	})
	require.NoError(t, err)
	return store
}

func redisAttemptLimiter(t *testing.T, window time.Duration) *authsvc.CacheAttemptLimiter {
	t.Helper()
	testutil.TestRedis(t)
	limiter, err := authsvc.NewCacheAttemptLimiter(authsvc.AttemptLimiterDeps{
		Cache:  facades.Cache(),
		Window: window,
	})
	require.NoError(t, err)
	return limiter
}

func TestCache_TOTPChallengeStore_IssueAndResolve(t *testing.T) {
	store := redisChallengeStore(t, time.Minute)
	userID := uuid.New()

	token, err := store.Issue(userID)
	require.NoError(t, err)
	t.Cleanup(func() { store.Revoke(token) })

	require.NotEmpty(t, token)
	assert.NotEqual(t, 2, strings.Count(token, "."), "the challenge must not look like a JWT")
	resolved, ok, err := store.Resolve(token)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, userID, resolved.UserID)
	assert.WithinDuration(t, time.Now(), resolved.IssuedAt, 2*time.Second)

	again, ok, err := store.Resolve(token)
	require.NoError(t, err)
	assert.True(t, ok, "resolving does not spend the challenge")
	assert.Equal(t, userID, again.UserID)
}

func TestCache_TOTPChallengeStore_KeysDoNotContainTheToken(t *testing.T) {
	client := testutil.TestRedis(t)
	store := redisChallengeStore(t, time.Minute)

	token, err := store.Issue(uuid.New())
	require.NoError(t, err)
	t.Cleanup(func() { store.Revoke(token) })

	keys, err := client.Keys(t.Context(), "*"+token+"*").Result()
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestCache_TOTPChallengeStore_ConsumeIsSingleUse(t *testing.T) {
	store := redisChallengeStore(t, time.Minute)
	token, err := store.Issue(uuid.New())
	require.NoError(t, err)

	assert.True(t, store.Consume(token))
	assert.False(t, store.Consume(token))
	_, ok, err := store.Resolve(token)
	require.NoError(t, err)
	assert.False(t, ok, "a consumed challenge no longer resolves")
}

func TestCache_TOTPChallengeStore_ConcurrentConsumeHasOneWinner(t *testing.T) {
	store := redisChallengeStore(t, time.Minute)
	token, err := store.Issue(uuid.New())
	require.NoError(t, err)

	const racers = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if store.Consume(token) {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, winners)
}

func TestCache_TOTPChallengeStore_Expires(t *testing.T) {
	store := redisChallengeStore(t, time.Second)
	token, err := store.Issue(uuid.New())
	require.NoError(t, err)

	time.Sleep(1500 * time.Millisecond)

	_, ok, err := store.Resolve(token)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCache_TOTPChallengeStore_RevokeAndUnknownTokens(t *testing.T) {
	store := redisChallengeStore(t, time.Minute)
	token, err := store.Issue(uuid.New())
	require.NoError(t, err)

	store.Revoke(token)
	store.Revoke(token)
	store.Revoke("")

	_, ok, err := store.Resolve(token)
	require.NoError(t, err)
	assert.False(t, ok)
	_, ok, err = store.Resolve("")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.False(t, store.Consume(""))
}

func TestCache_TOTPChallengeStore_ValidatesInput(t *testing.T) {
	_, err := authsvc.NewCacheTOTPChallengeStore(authsvc.ChallengeStoreDeps{TTL: time.Minute})
	assert.Error(t, err)
	_, err = authsvc.NewCacheTOTPChallengeStore(authsvc.ChallengeStoreDeps{
		Cache: facades.Cache(),
		TTL:   0,
	})
	assert.Error(t, err)

	store := redisChallengeStore(t, time.Minute)
	_, err = store.Issue(uuid.Nil)
	assert.Error(t, err)
}

func TestCache_AttemptLimiter_CountsAndResets(t *testing.T) {
	limiter := redisAttemptLimiter(t, time.Minute)
	userID := uuid.New()
	t.Cleanup(func() { limiter.Reset(userID) })

	for want := int64(1); want <= 3; want++ {
		got, err := limiter.Claim(userID)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	limiter.Reset(userID)
	got, err := limiter.Claim(userID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got)
}

func TestCache_AttemptLimiter_ConcurrentClaimsAreAllCounted(t *testing.T) {
	limiter := redisAttemptLimiter(t, time.Minute)
	userID := uuid.New()
	t.Cleanup(func() { limiter.Reset(userID) })

	const racers = 20
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := limiter.Claim(userID)
			require.NoError(t, err)
		}()
	}
	wg.Wait()

	got, err := limiter.Claim(userID)
	require.NoError(t, err)
	assert.Equal(t, int64(racers+1), got)
}

func TestCache_AttemptLimiter_WindowLapses(t *testing.T) {
	limiter := redisAttemptLimiter(t, time.Second)
	userID := uuid.New()
	t.Cleanup(func() { limiter.Reset(userID) })

	_, err := limiter.Claim(userID)
	require.NoError(t, err)
	_, err = limiter.Claim(userID)
	require.NoError(t, err)

	time.Sleep(1500 * time.Millisecond)

	got, err := limiter.Claim(userID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got)
}
