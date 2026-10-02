package deposit

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
)

// addressCacheTestRedisDB is the logical database .env.testing assigns to tests; the
// API uses 0, so these tests can never touch its watched-address sets.
const addressCacheTestRedisDB = 15

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	client := redis.NewClient(&redis.Options{Addr: host + ":" + port, DB: addressCacheTestRedisDB})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("redis unavailable for address cache tests: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newCacheTestService(t *testing.T, rdb *redis.Client) *Service {
	t.Helper()
	return NewService(rdb, chain.NewRegistry(), newWebhookSvc(), repositories.NewAddressRepository(nil), repositories.NewTransactionRepository(nil), nil)
}

func cachedMembers(t *testing.T, rdb *redis.Client, chainID string) []string {
	t.Helper()
	members, err := rdb.SMembers(context.Background(), addressCacheKey(chainID)).Result()
	if err != nil {
		t.Fatalf("smembers: %v", err)
	}
	sort.Strings(members)
	return members
}

func TestSyncAddressCache_RebuildsAStaleSet(t *testing.T) {
	mocks.TestDB(t)
	rdb := testRedis(t)
	ctx := context.Background()
	const chainID = "eth"
	key := addressCacheKey(chainID)
	t.Cleanup(func() { rdb.Del(context.Background(), key) })

	w := mocks.InsertWallet(t, chainID)
	mocks.InsertAddress(t, w.ID, chainID, "0x00000000000000000000000000000000000000a1", "user_a", 1)
	mocks.InsertAddress(t, w.ID, chainID, "0x00000000000000000000000000000000000000b2", "user_b", 2)
	if err := rdb.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.SAdd(ctx, key, "0xstale").Err(); err != nil {
		t.Fatal(err)
	}
	svc := newCacheTestService(t, rdb)

	rebuilt, err := svc.SyncAddressCache(ctx, chainID)
	if err != nil || !rebuilt {
		t.Fatalf("expected a rebuild, got rebuilt=%v err=%v", rebuilt, err)
	}
	active, err := repositories.NewAddressRepository(nil).PluckActiveAddresses(context.Background(), chainID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(active)
	got := cachedMembers(t, rdb, chainID)
	if len(got) != len(active) {
		t.Fatalf("cache %v, want the active addresses %v", got, active)
	}
	for i := range active {
		if got[i] != active[i] {
			t.Fatalf("cache %v, want the active addresses %v", got, active)
		}
	}

	rebuilt, err = svc.SyncAddressCache(ctx, chainID)
	if err != nil || rebuilt {
		t.Fatalf("an in-sync cache must be left alone, got rebuilt=%v err=%v", rebuilt, err)
	}
}

func TestSyncAddressCache_SameSizeDifferentMembersIsStale(t *testing.T) {
	mocks.TestDB(t)
	rdb := testRedis(t)
	ctx := context.Background()
	const chainID = "eth"
	key := addressCacheKey(chainID)
	t.Cleanup(func() { rdb.Del(context.Background(), key) })

	w := mocks.InsertWallet(t, chainID)
	active, err := repositories.NewAddressRepository(nil).PluckActiveAddresses(context.Background(), chainID)
	if err != nil {
		t.Fatal(err)
	}
	mocks.InsertAddress(t, w.ID, chainID, "0x00000000000000000000000000000000000000c3", "user_c", 1)
	stale := append(append([]string(nil), active...), "0xnot-in-the-database")
	if err := rdb.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.SAdd(ctx, key, toMembers(stale)...).Err(); err != nil {
		t.Fatal(err)
	}

	rebuilt, err := newCacheTestService(t, rdb).SyncAddressCache(ctx, chainID)
	if err != nil || !rebuilt {
		t.Fatalf("expected a rebuild for a same-size set with a foreign member, got rebuilt=%v err=%v", rebuilt, err)
	}
	isMember, err := rdb.SIsMember(ctx, key, "0x00000000000000000000000000000000000000c3").Result()
	if err != nil || !isMember {
		t.Fatalf("the new address must be watched after the rebuild, got %v err=%v", isMember, err)
	}
}

func TestRefreshAddressCache_ClearsTheSetWhenNoAddressIsActive(t *testing.T) {
	mocks.TestDB(t)
	rdb := testRedis(t)
	ctx := context.Background()
	const chainID = "btc"
	key := addressCacheKey(chainID)
	t.Cleanup(func() { rdb.Del(context.Background(), key) })
	if err := rdb.SAdd(ctx, key, "tb1qstale").Err(); err != nil {
		t.Fatal(err)
	}
	active, err := repositories.NewAddressRepository(nil).PluckActiveAddresses(context.Background(), chainID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Skipf("fresh test database already has %d active btc addresses", len(active))
	}

	if err := newCacheTestService(t, rdb).RefreshAddressCache(ctx, chainID); err != nil {
		t.Fatalf("RefreshAddressCache: %v", err)
	}
	if got := cachedMembers(t, rdb, chainID); len(got) != 0 {
		t.Fatalf("expected an empty set, got %v", got)
	}
}

func TestIsWatchedAddress_FallsBackToTheDatabaseWhenRedisFails(t *testing.T) {
	mocks.TestDB(t)
	const chainID = "eth"
	const address = "0x00000000000000000000000000000000000000d4"
	w := mocks.InsertWallet(t, chainID)
	mocks.InsertAddress(t, w.ID, chainID, address, "user_d", 1)
	unreachable := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = unreachable.Close() })
	svc := newCacheTestService(t, unreachable)

	watched, err := svc.isWatchedAddress(context.Background(), chainID, address)
	if err != nil || !watched {
		t.Fatalf("expected the database to confirm the address, got watched=%v err=%v", watched, err)
	}
	watched, err = svc.isWatchedAddress(context.Background(), chainID, "0x00000000000000000000000000000000000000e5")
	if err != nil || watched {
		t.Fatalf("an unknown address must not be watched, got watched=%v err=%v", watched, err)
	}
}
