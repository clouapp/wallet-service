package lock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/withdraw"
)

// Client runs SETNX, DEL, GET, INCR+EXPIRE, and INCRBY for the daily spend counter.
// The service keeps the keys, the lock value, and the thresholds. Values are not logged.
type Client struct {
	client *redis.Client
}

var _ withdraw.Locker = (*Client)(nil)

// New wraps client. A nil client returns a nil locker so the service keeps its nil-client path.
func New(client *redis.Client) withdraw.Locker {
	if client == nil {
		return nil
	}
	return &Client{client: client}
}

// SetNX sets key to value only when it is absent. The Redis error is returned unchanged.
func (c *Client) SetNX(ctx context.Context, key, value string, expiration time.Duration) (bool, error) {
	if c == nil || c.client == nil {
		return false, fmt.Errorf("redis lock: client is nil")
	}
	return c.client.SetNX(ctx, key, value, expiration).Result()
}

// Del removes key. The Redis error is returned unchanged.
func (c *Client) Del(ctx context.Context, key string) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis lock: client is nil")
	}
	return c.client.Del(ctx, key).Err()
}

// Int reads an integer. A missing key returns 0 and a nil error; every other error is unchanged.
func (c *Client) Int(ctx context.Context, key string) (int, error) {
	if c == nil || c.client == nil {
		return 0, fmt.Errorf("redis lock: client is nil")
	}
	count, err := c.client.Get(ctx, key).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return count, err
}

// IncrExpire increments key and sets its TTL in one pipeline, matching the previous two commands.
func (c *Client) IncrExpire(ctx context.Context, key string, expiration time.Duration) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis lock: client is nil")
	}
	pipe := c.client.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, expiration)
	_, err := pipe.Exec(ctx)
	return err
}

// IncrBy adds delta and returns the new total. The first increment of a key
// sets expiration. A non-positive delta is refused so a caller cannot store
// a negative counter.
func (c *Client) IncrBy(ctx context.Context, key string, delta int64, expiration time.Duration) (int64, error) {
	if c == nil || c.client == nil {
		return 0, fmt.Errorf("redis lock: client is nil")
	}
	if delta <= 0 {
		return 0, fmt.Errorf("redis lock: delta must be positive")
	}
	total, err := c.client.IncrBy(ctx, key, delta).Result()
	if err != nil {
		return 0, err
	}
	if total == delta {
		_ = c.client.Expire(ctx, key, expiration).Err()
	}
	return total, nil
}

// DecrBy subtracts delta. A non-positive delta is refused.
func (c *Client) DecrBy(ctx context.Context, key string, delta int64) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis lock: client is nil")
	}
	if delta <= 0 {
		return fmt.Errorf("redis lock: delta must be positive")
	}
	return c.client.DecrBy(ctx, key, delta).Err()
}
