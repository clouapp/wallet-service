package pricecache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/price"
)

// Cache reads and writes a currency price with GET and SET.
// The service keeps the key, the TTL, and the JSON number. Values are not logged.
type Cache struct {
	client *redis.Client
}

var _ price.PriceCache = (*Cache)(nil)

// New wraps client. A nil client returns a nil cache so the service keeps its nil-client path.
func New(client *redis.Client) price.PriceCache {
	if client == nil {
		return nil
	}
	return &Cache{client: client}
}

// Float64 reads key with GET and parses it as a float. The Redis error is returned unchanged.
func (c *Cache) Float64(ctx context.Context, key string) (float64, error) {
	if c == nil || c.client == nil {
		return 0, fmt.Errorf("redis price cache: client is nil")
	}
	return c.client.Get(ctx, key).Float64()
}

// Set writes key with SET and the given TTL. The Redis error is returned unchanged.
func (c *Cache) Set(ctx context.Context, key string, value []byte, expiration time.Duration) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis price cache: client is nil")
	}
	return c.client.Set(ctx, key, value, expiration).Err()
}
