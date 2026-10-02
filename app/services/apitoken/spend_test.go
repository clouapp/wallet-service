package apitoken_test

import (
	"context"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/apitoken"
	"github.com/macrowallets/waas/tests/testutil"
)

func TestReserveDailyUSDUsesANamespacedCounter(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + time.Now().UTC().Format("2006-01-02")
	ctx := context.Background()

	if err := apitoken.ReserveDailyUSD(ctx, client, key, `{"daily_usd":10}`, "usdt", "4"); err != nil {
		t.Fatal(err)
	}
	if err := apitoken.ReserveDailyUSD(ctx, client, key, `{"daily_usd":10}`, "usdt", "7"); err == nil {
		t.Fatal("second spend should cross the daily cap")
	}
	if err := apitoken.ReserveDailyUSD(ctx, nil, key, "{}", "eth", "1"); err != nil {
		t.Fatal(err)
	}
}
