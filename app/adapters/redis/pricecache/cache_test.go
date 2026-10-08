package pricecache

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestNew_Returns_NilForANilClient(t *testing.T) {
	if New(nil) != nil {
		t.Fatal("expected a nil price cache when Redis is not configured")
	}
}

func TestNil_Cache_ReportsAMissingClient(t *testing.T) {
	var cache *Cache
	if _, err := cache.Get(context.Background(), "currency:BTC"); err == nil {
		t.Fatal("expected error for a nil cache read")
	}
	if err := cache.Set(context.Background(), "currency:BTC", []byte("1"), time.Second); err == nil {
		t.Fatal("expected error for a nil cache write")
	}
}

func TestSet_Writes_TheBytesAndKeepsTheTTL(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "currency:BTC"
	ctx := context.Background()
	value := []byte("42.5")

	if err := New(client).Set(ctx, key, value, 45*time.Second); err != nil {
		t.Fatalf("set: %v", err)
	}

	stored, err := client.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(stored, value) {
		t.Fatal("SET value changed")
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 30*time.Second || ttl > 45*time.Second {
		t.Fatalf("ttl = %s", ttl)
	}
}

func TestGet_Reads_TheStoredDecimalText(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "currency:ETH"
	ctx := context.Background()
	cache := New(client)
	if err := cache.Set(ctx, key, []byte("3200"), time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := cache.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "3200" {
		t.Fatalf("value = %q", got)
	}
}

func TestGet_Missing_KeyIsRedisNil(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)

	_, err := New(client).Get(context.Background(), prefix+"currency:missing")
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("error = %v", err)
	}
}

func TestCommands_Canceled_Context(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cache := New(client)

	if _, err := cache.Get(ctx, prefix+"currency:BTC"); !errors.Is(err, context.Canceled) {
		t.Fatalf("get error = %v", err)
	}
	if err := cache.Set(ctx, prefix+"currency:BTC", []byte("1"), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("set error = %v", err)
	}
}
