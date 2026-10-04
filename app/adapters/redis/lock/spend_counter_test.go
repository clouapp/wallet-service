package lock

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/tests/testutil"
)

func TestIncrByOnTheTestRedisKeepsADailyTTL(t *testing.T) {
	t.Setenv("REDIS_DB", "15")
	t.Setenv("TEST_REDIS_ADDR", "localhost:6380")
	client := testutil.TestRedis(t)
	locker := New(client)
	if locker == nil {
		t.Fatal("redis client was nil")
	}

	key := "vault:quota:spend:" + uuid.NewString() + ":2099-01-01"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = client.Del(ctx, key).Err()
	})

	ctx := context.Background()
	total, err := locker.IncrBy(ctx, key, 2500, 24*time.Hour)
	if err != nil {
		t.Fatalf("incr: %v", err)
	}
	if total != 2500 {
		t.Fatalf("total = %d, want 2500", total)
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		t.Fatalf("ttl = %s, want a 24h window", ttl)
	}

	total, err = locker.IncrBy(ctx, key, 1000, 24*time.Hour)
	if err != nil {
		t.Fatalf("second incr: %v", err)
	}
	if total != 3500 {
		t.Fatalf("total = %d, want 3500", total)
	}
	if err := locker.DecrBy(ctx, key, 1000); err != nil {
		t.Fatalf("decr: %v", err)
	}
	left, err := client.Get(ctx, key).Int64()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if left != 2500 {
		t.Fatalf("after refund = %d, want 2500", left)
	}
}
