package lock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/tests/testutil"
)

func TestNewReturnsNilForANilClient(t *testing.T) {
	if New(nil) != nil {
		t.Fatal("expected a nil locker when Redis is not configured")
	}
}

func TestNilClientReportsAMissingClient(t *testing.T) {
	var client *Client
	if _, err := client.SetNX(context.Background(), "vault:lock:withdrawal:x", "1", time.Second); err == nil {
		t.Fatal("expected error for a nil client")
	}
	if err := client.Del(context.Background(), "vault:lock:withdrawal:x"); err == nil {
		t.Fatal("expected error for a nil client delete")
	}
	if _, err := client.Int(context.Background(), "vault:ratelimit:passphrase:x"); err == nil {
		t.Fatal("expected error for a nil client read")
	}
	if err := client.IncrExpire(context.Background(), "vault:ratelimit:passphrase:x", time.Second); err == nil {
		t.Fatal("expected error for a nil client counter")
	}
}

func TestSetNXWritesTheValueAndKeepsTheTTL(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "lock"
	ctx := context.Background()
	locker := New(client)

	acquired, err := locker.SetNX(ctx, key, "1", 45*time.Second)
	if err != nil {
		t.Fatalf("setnx: %v", err)
	}
	if !acquired {
		t.Fatal("expected the first SETNX to acquire the key")
	}
	again, err := locker.SetNX(ctx, key, "1", 45*time.Second)
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

func TestDelRemovesTheKey(t *testing.T) {
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

func TestIntMissingKeyIsZero(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	count, err := New(client).Int(context.Background(), prefix+"missing")
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d", count)
	}
}

func TestIntRejectsANonInteger(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "bad"
	ctx := context.Background()
	if err := client.Set(ctx, key, "nope", time.Minute).Err(); err != nil {
		t.Fatalf("set: %v", err)
	}

	_, err := New(client).Int(ctx, key)
	if err == nil {
		t.Fatal("expected a non-integer to fail")
	}
}

func TestIncrExpireCountsAndSetsTTL(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "attempts"
	ctx := context.Background()
	locker := New(client)

	if err := locker.IncrExpire(ctx, key, 45*time.Second); err != nil {
		t.Fatalf("first incr: %v", err)
	}
	if err := locker.IncrExpire(ctx, key, 45*time.Second); err != nil {
		t.Fatalf("second incr: %v", err)
	}
	count, err := locker.Int(ctx, key)
	if err != nil {
		t.Fatalf("int: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d", count)
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 30*time.Second || ttl > 45*time.Second {
		t.Fatalf("ttl = %s", ttl)
	}
}

func TestSetNXCanceledContext(t *testing.T) {
	client := testutil.TestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New(client).SetNX(ctx, "vault:lock:withdrawal:canceled", "1", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
