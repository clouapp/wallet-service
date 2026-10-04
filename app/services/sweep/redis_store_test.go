package sweep

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisStore is the test stand-in for the sweep Redis adapter. This package's
// tests cannot import app/adapters, and the commands stay the same as production.
type redisStore struct {
	client *redis.Client
}

func (s redisStore) SetNX(ctx context.Context, key, value string, expiration time.Duration) (bool, error) {
	return s.client.SetNX(ctx, key, value, expiration).Result()
}

func (s redisStore) Del(ctx context.Context, key string) error {
	return s.client.Del(ctx, key).Err()
}

func (s redisStore) Incr(ctx context.Context, key string) (int64, error) {
	return s.client.Incr(ctx, key).Result()
}

func (s redisStore) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return s.client.Expire(ctx, key, expiration).Err()
}
