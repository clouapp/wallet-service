package apitoken_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/apitoken"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

type redisDailyCounter struct{ client *redis.Client }

func (c redisDailyCounter) IncrByFloat(ctx context.Context, key string, value float64) (float64, error) {
	return c.client.IncrByFloat(ctx, key, value).Result()
}

func (c redisDailyCounter) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.client.Expire(ctx, key, ttl).Err()
}

func TestReserveDailyUSDUsesANamespacedCounter(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + time.Now().UTC().Format("2006-01-02")
	ctx := context.Background()

	counter := redisDailyCounter{client: client}
	if err := apitoken.ReserveDailyUSD(ctx, counter, key, `{"daily_usd":10}`, "usdt", "4"); err != nil {
		t.Fatal(err)
	}
	if err := apitoken.ReserveDailyUSD(ctx, counter, key, `{"daily_usd":10}`, "usdt", "7"); err == nil {
		t.Fatal("second spend should cross the daily cap")
	}
	if err := apitoken.ReserveDailyUSD(ctx, nil, key, "{}", "eth", "1"); err != nil {
		t.Fatal(err)
	}
}
