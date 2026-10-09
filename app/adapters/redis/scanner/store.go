package scanner

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/deposit"
)

// Store runs the deposit scanner's Redis commands: GET, SET, SISMEMBER, SCARD,
// SMISMEMBER, and a DEL+SADD transaction. The service keeps the keys. Values are not logged.
type Store struct {
	client redis.UniversalClient
}

var _ deposit.RedisStore = (*Store)(nil)

// New wraps client. A nil client returns a nil store so the service keeps its nil-client path.
func New(client redis.UniversalClient) deposit.RedisStore {
	if client == nil {
		return nil
	}
	return &Store{client: client}
}

// Uint64 reads key with GET and parses it as uint64. The Redis error is returned unchanged.
func (s *Store) Uint64(ctx context.Context, key string) (uint64, error) {
	if s == nil || s.client == nil {
		return 0, fmt.Errorf("redis scanner: client is nil")
	}
	return s.client.Get(ctx, key).Uint64()
}

// Set writes key with SET and the given TTL. The Redis error is returned unchanged.
func (s *Store) Set(ctx context.Context, key string, value uint64, expiration time.Duration) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis scanner: client is nil")
	}
	return s.client.Set(ctx, key, value, expiration).Err()
}

// SIsMember reports whether member is in key. The Redis error is returned unchanged.
func (s *Store) SIsMember(ctx context.Context, key, member string) (bool, error) {
	if s == nil || s.client == nil {
		return false, fmt.Errorf("redis scanner: client is nil")
	}
	return s.client.SIsMember(ctx, key, member).Result()
}

// SCard returns the cardinality of key. The Redis error is returned unchanged.
func (s *Store) SCard(ctx context.Context, key string) (int64, error) {
	if s == nil || s.client == nil {
		return 0, fmt.Errorf("redis scanner: client is nil")
	}
	return s.client.SCard(ctx, key).Result()
}

// SMIsMember reports membership of each member. The Redis error is returned unchanged.
func (s *Store) SMIsMember(ctx context.Context, key string, members ...any) ([]bool, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("redis scanner: client is nil")
	}
	return s.client.SMIsMember(ctx, key, members...).Result()
}

// ReplaceSet deletes key and, when members is non-empty, adds them in one MULTI/EXEC.
// The Redis error is returned unchanged.
func (s *Store) ReplaceSet(ctx context.Context, key string, members ...any) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis scanner: client is nil")
	}
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, key)
	if len(members) > 0 {
		pipe.SAdd(ctx, key, members...)
	}
	_, err := pipe.Exec(ctx)
	return err
}
