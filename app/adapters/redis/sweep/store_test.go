package sweep

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestNew_Returns_NilForANilClient(t *testing.T) {
	if New(nil) != nil {
		t.Fatal("expected a nil sweep store when Redis is not configured")
	}
}

func TestNil_Store_ReportsAMissingClient(t *testing.T) {
	var store *Store
	ctx := context.Background()
	if _, err := store.SetNX(ctx, "vault:lock:wallet_ops:x", "1", time.Second); err == nil {
		t.Fatal("expected error for a nil store lock")
	}
	if err := store.Del(ctx, "vault:lock:wallet_ops:x"); err == nil {
		t.Fatal("expected error for a nil store delete")
	}
	if _, err := store.Incr(ctx, "vault:quota:consolidate:x"); err == nil {
		t.Fatal("expected error for a nil store counter")
	}
	if err := store.Expire(ctx, "vault:quota:consolidate:x", time.Second); err == nil {
		t.Fatal("expected error for a nil store expiry")
	}
}

func TestSet_NX_WritesTheValueAndKeepsTheTTL(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "lock"
	ctx := context.Background()
	store := New(client)

	acquired, err := store.SetNX(ctx, key, "1", 45*time.Second)
	if err != nil {
		t.Fatalf("setnx: %v", err)
	}
	if !acquired {
		t.Fatal("expected the first SETNX to acquire the key")
	}
	again, err := store.SetNX(ctx, key, "1", 45*time.Second)
	if err != nil {
		t.Fatalf("second setnx: %v", err)
	}
	if again {
		t.Fatal("expected the second SETNX to leave the key")
	}

	stored, err := client.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored != "1" {
		t.Fatal("SETNX value changed")
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 30*time.Second || ttl > 45*time.Second {
		t.Fatalf("ttl = %s", ttl)
	}
}

func TestDel_Removes_TheKey(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "lock"
	ctx := context.Background()
	if err := client.Set(ctx, key, "1", time.Minute).Err(); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := New(client).Del(ctx, key); err != nil {
		t.Fatalf("del: %v", err)
	}
	_, err := client.Get(ctx, key).Result()
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("key still present: %v", err)
	}
}

func TestIncr_Does_NotSetATTL(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "quota"
	ctx := context.Background()
	store := New(client)

	first, err := store.Incr(ctx, key)
	if err != nil {
		t.Fatalf("first incr: %v", err)
	}
	if first != 1 {
		t.Fatalf("count = %d", first)
	}
	second, err := store.Incr(ctx, key)
	if err != nil {
		t.Fatalf("second incr: %v", err)
	}
	if second != 2 {
		t.Fatalf("count = %d", second)
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl != -1 {
		t.Fatalf("ttl = %s, want no expiry", ttl)
	}
}

func TestExpire_Sets_TheTTL(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "quota"
	ctx := context.Background()
	store := New(client)
	if _, err := store.Incr(ctx, key); err != nil {
		t.Fatalf("incr: %v", err)
	}

	if err := store.Expire(ctx, key, 45*time.Second); err != nil {
		t.Fatalf("expire: %v", err)
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 30*time.Second || ttl > 45*time.Second {
		t.Fatalf("ttl = %s", ttl)
	}
}

func TestSet_NX_CanceledContext(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New(client).SetNX(ctx, prefix+"lock", "1", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
