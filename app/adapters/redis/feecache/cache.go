package feecache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/feeestimate"
)

// Cache stores fee estimates in Redis with a per-key expiry.
// A nil client is a no-op so a process without Redis still estimates.
type Cache struct {
	client *redis.Client
}

var _ feeestimate.Cache = (*Cache)(nil)

// New wraps client. A nil client still returns a cache; Get misses and Set does nothing.
func New(client *redis.Client) *Cache {
	return &Cache{client: client}
}

// Get reads key. A missing key is a miss, not an error.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if c == nil || c.client == nil {
		return nil, false, nil
	}
	value, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// Set writes key when ttl is positive. A nil client or a non-positive ttl is a no-op.
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c == nil || c.client == nil || ttl <= 0 {
		return nil
	}
	return c.client.Set(ctx, key, value, ttl).Err()
}
