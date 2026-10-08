package deposit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisStore is the test stand-in for the scanner adapter. This package's tests
// cannot import app/adapters, and the commands stay the same as production.
type redisStore struct {
	client *redis.Client
}

func (s redisStore) Uint64(ctx context.Context, key string) (uint64, error) {
	return s.client.Get(ctx, key).Uint64()
}

func (s redisStore) Set(ctx context.Context, key string, value uint64, expiration time.Duration) error {
	return s.client.Set(ctx, key, value, expiration).Err()
}

func (s redisStore) SIsMember(ctx context.Context, key, member string) (bool, error) {
	return s.client.SIsMember(ctx, key, member).Result()
}

func (s redisStore) SCard(ctx context.Context, key string) (int64, error) {
	return s.client.SCard(ctx, key).Result()
}

func (s redisStore) SMIsMember(ctx context.Context, key string, members ...any) ([]bool, error) {
	return s.client.SMIsMember(ctx, key, members...).Result()
}

func (s redisStore) ReplaceSet(ctx context.Context, key string, members ...any) error {
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, key)
	if len(members) > 0 {
		pipe.SAdd(ctx, key, members...)
	}
	_, err := pipe.Exec(ctx)
	return err
}
