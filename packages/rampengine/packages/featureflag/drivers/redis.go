package drivers

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// Redis persists feature values in Redis: one hash per feature (fields are
// scope keys) plus a set naming features that have stored values (for Purge).
// The go-redis client is injected; the DriverCreator hands it the connection
// goravel/redis already manages.
//
// Set, SetIfAbsent and Purge pipeline writes to two differently-prefixed keys
// (the per-feature hash and the feature-name set) in a MULTI/EXEC transaction.
// This targets a single Redis instance; under Redis Cluster those keys may land
// in different hash slots and the transaction would fail with CROSSSLOT. Add a
// shared hash tag to both key builders if cluster support is ever required.
//
// A store built WithTTL expires each hash field individually (HEXPIRE, Redis
// 7.4+). Because HSET clears a field's TTL, every write path re-applies it.
type Redis struct {
	client goredis.UniversalClient
	prefix string
	ttl    time.Duration
}

// RedisOption configures a Redis.
type RedisOption func(*Redis)

// WithTTL expires every stored field after d. It exists for the cache role in a
// Cached, where entries must recycle; a Redis used as the primary
// store must not have one, or stored overrides would disappear on their own.
// A non-positive d means no expiry, which is the default.
//
// Expiry is per hash field (HEXPIRE), so a positive d requires Redis 7.4+.
func WithTTL(d time.Duration) RedisOption {
	return func(s *Redis) { s.ttl = d }
}

// NewRedis returns a Redis. An empty prefix defaults to "feature_flag".
func NewRedis(client goredis.UniversalClient, prefix string, opts ...RedisOption) *Redis {
	if prefix == "" {
		prefix = "feature_flag"
	}
	s := &Redis{client: client, prefix: prefix}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Redis) hashKey(feature string) string { return s.prefix + ":feature:" + feature }
func (s *Redis) featuresKey() string           { return s.prefix + ":features" }

// expire queues an HEXPIRE for fields onto pipe. It is called from every write
// path rather than once at insert because HSET clears a field's TTL: a bulk
// rewrite would otherwise leave the whole feature cached forever.
func (s *Redis) expire(ctx context.Context, pipe goredis.Pipeliner, feature string, fields ...string) {
	if s.ttl <= 0 || len(fields) == 0 {
		return
	}
	pipe.HExpire(ctx, s.hashKey(feature), s.ttl, fields...)
}

func (s *Redis) Get(ctx context.Context, feature, scopeKey string) (any, bool, error) {
	raw, err := s.client.HGet(ctx, s.hashKey(feature), scopeKey).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("featureflag: redis hget: %w", err)
	}
	v, err := decodeValue(raw)
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (s *Redis) Set(ctx context.Context, feature, scopeKey string, value any) error {
	raw, err := encodeValue(value)
	if err != nil {
		return err
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, s.hashKey(feature), scopeKey, raw)
	pipe.SAdd(ctx, s.featuresKey(), feature)
	s.expire(ctx, pipe, feature, scopeKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("featureflag: redis set: %w", err)
	}
	return nil
}

func (s *Redis) SetIfAbsent(ctx context.Context, feature, scopeKey string, value any) (bool, error) {
	raw, err := encodeValue(value)
	if err != nil {
		return false, err
	}
	pipe := s.client.TxPipeline()
	setnx := pipe.HSetNX(ctx, s.hashKey(feature), scopeKey, raw)
	pipe.SAdd(ctx, s.featuresKey(), feature)
	// Unconditional: a TxPipeline cannot branch on the HSETNX result. A losing
	// call therefore refreshes the winner's TTL, which is harmless for a cache
	// -- and the Cached never calls this on its cache leg anyway.
	s.expire(ctx, pipe, feature, scopeKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("featureflag: redis setnx: %w", err)
	}
	return setnx.Val(), nil
}

func (s *Redis) SetForAllScopes(ctx context.Context, feature string, value any) error {
	raw, err := encodeValue(value)
	if err != nil {
		return err
	}
	fields, err := s.client.HKeys(ctx, s.hashKey(feature)).Result()
	if err != nil {
		return fmt.Errorf("featureflag: redis hkeys: %w", err)
	}
	if len(fields) == 0 {
		return nil
	}
	pairs := make([]any, 0, len(fields)*2)
	for _, f := range fields {
		pairs = append(pairs, f, raw)
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, s.hashKey(feature), pairs...)
	s.expire(ctx, pipe, feature, fields...)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("featureflag: redis hset: %w", err)
	}
	return nil
}

func (s *Redis) Delete(ctx context.Context, feature, scopeKey string) error {
	if err := s.client.HDel(ctx, s.hashKey(feature), scopeKey).Err(); err != nil {
		return fmt.Errorf("featureflag: redis hdel: %w", err)
	}
	return nil
}

func (s *Redis) Purge(ctx context.Context, features []string) error {
	if len(features) == 0 {
		members, err := s.client.SMembers(ctx, s.featuresKey()).Result()
		if err != nil {
			return fmt.Errorf("featureflag: redis smembers: %w", err)
		}
		features = members
	}
	if len(features) == 0 {
		return nil
	}
	pipe := s.client.TxPipeline()
	for _, f := range features {
		pipe.Del(ctx, s.hashKey(f))
		pipe.SRem(ctx, s.featuresKey(), f)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("featureflag: redis purge: %w", err)
	}
	return nil
}

var _ contracts.Driver = (*Redis)(nil)
