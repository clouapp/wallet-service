package sweep

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	sweepsvc "github.com/macrowallets/waas/app/services/sweep"
)

// Store runs SETNX, DEL, INCR, and EXPIRE. The service keeps the keys, the
// lock value, and the TTLs. Values are not logged.
type Store struct {
	client *redis.Client
}

var _ sweepsvc.RedisStore = (*Store)(nil)

// New wraps client. A nil client returns a nil store so the service keeps its nil-client path.
func New(client *redis.Client) sweepsvc.RedisStore {
	if client == nil {
		return nil
	}
	return &Store{client: client}
}

// SetNX sets key to value only when it is absent. The Redis error is returned unchanged.
func (s *Store) SetNX(ctx context.Context, key, value string, expiration time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, fmt.Errorf("redis sweep: client is nil")
	}
	return s.client.SetNX(ctx, key, value, expiration).Result()
}

// Del removes key. The Redis error is returned unchanged.
func (s *Store) Del(ctx context.Context, key string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis sweep: client is nil")
	}
	return s.client.Del(ctx, key).Err()
}

// Incr increments key and returns the new count. The Redis error is returned unchanged.
func (s *Store) Incr(ctx context.Context, key string) (int64, error) {
	if s == nil || s.client == nil {
		return 0, fmt.Errorf("redis sweep: client is nil")
	}
	return s.client.Incr(ctx, key).Result()
}

// Expire sets the TTL of key. The Redis error is returned unchanged.
func (s *Store) Expire(ctx context.Context, key string, expiration time.Duration) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis sweep: client is nil")
	}
	return s.client.Expire(ctx, key, expiration).Err()
}
