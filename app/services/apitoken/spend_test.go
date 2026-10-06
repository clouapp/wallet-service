package apitoken_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/apitoken"
)

type memDailyCounter struct {
	mu   sync.Mutex
	vals map[string]float64
}

func (c *memDailyCounter) IncrByFloat(_ context.Context, key string, value float64) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vals == nil {
		c.vals = map[string]float64{}
	}
	c.vals[key] += value
	return c.vals[key], nil
}

func (c *memDailyCounter) Expire(context.Context, string, time.Duration) error { return nil }

func TestReserve_Daily_USDUsesANamespacedCounter(t *testing.T) {
	key := "apitoken:" + time.Now().UTC().Format("2006-01-02")
	ctx := context.Background()

	counter := &memDailyCounter{}
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
