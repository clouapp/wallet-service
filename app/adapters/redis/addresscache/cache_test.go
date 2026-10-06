package addresscache

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestNewReturnsNilForANilClient(t *testing.T) {
	if New(nil) != nil {
		t.Fatal("expected a nil address cache when Redis is not configured")
	}
}

func TestNilCacheReportsAMissingClient(t *testing.T) {
	var cache *Cache
	if err := cache.SAdd(context.Background(), "vault:addresses:eth", "0xabc"); err == nil {
		t.Fatal("expected error for a nil cache")
	}
}

func TestSAddRejectsMissingContextKeyAndMember(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cache := New(client)

	if err := cache.SAdd(nil, "vault:addresses:eth", "0xabc"); err == nil {
		t.Fatal("expected error for a nil context")
	}
	if err := cache.SAdd(context.Background(), "  ", "0xabc"); err == nil {
		t.Fatal("expected error for a blank key")
	}
	if err := cache.SAdd(context.Background(), "vault:addresses:eth"); err == nil {
		t.Fatal("expected error for a missing member")
	}
}

func TestSAddWritesTheMemberAndSetsNoTTL(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "vault:addresses:eth"
	ctx := context.Background()
	cache := New(client)

	if err := cache.SAdd(ctx, key, "0xabc"); err != nil {
		t.Fatalf("sadd: %v", err)
	}
	if err := cache.SAdd(ctx, key, "0xabc"); err != nil {
		t.Fatalf("second sadd: %v", err)
	}

	members, err := client.SMembers(ctx, key).Result()
	if err != nil {
		t.Fatalf("smembers: %v", err)
	}
	if len(members) != 1 || members[0] != "0xabc" {
		t.Fatalf("members = %v", members)
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl >= 0 {
		t.Fatalf("ttl = %s, SADD must not expire the key", ttl)
	}
}

func TestSAddCanceledContext(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := New(client).SAdd(ctx, prefix+"vault:addresses:eth", "0xabc")
	if err == nil {
		t.Fatal("expected SADD to fail on a canceled context")
	}
}

func TestSAddUsesTheGivenKey(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "other"
	ctx := context.Background()

	if err := New(client).SAdd(ctx, key, "tb1qabc"); err != nil {
		t.Fatalf("sadd: %v", err)
	}
	count, err := client.SCard(ctx, key).Result()
	if err != nil {
		t.Fatalf("scard: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d", count)
	}
}
