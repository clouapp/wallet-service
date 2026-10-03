package lock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/withdraw"
)

// Client runs SETNX, DEL, GET, and an INCR+EXPIRE pipeline.
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
