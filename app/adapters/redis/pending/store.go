package pending

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/redis/go-redis/v9"

	depositpending "github.com/macrowallets/waas/app/services/deposit/pending"
)

// DefaultRedisKeyPrefix namespaces the live keys: <prefix><chain> is a sorted set of
// blocks scored by next retry time (unix ms), <prefix><chain>:entries the entry JSON.
const DefaultRedisKeyPrefix = "vault:deposit_pending:"

// RedisStore persists pending deposit entries. The service keeps the Store port.
type RedisStore struct {
	rdb    *redis.Client
	prefix string
}

var _ depositpending.Store = (*RedisStore)(nil)

// RedisStoreDeps is the client and key prefix NewRedisStore stores.
// Redis must be set; an empty KeyPrefix is rejected.
type RedisStoreDeps struct {
	Redis     *redis.Client
	KeyPrefix string
}

func NewRedisStore(deps RedisStoreDeps) (*RedisStore, error) {
	if deps.Redis == nil {
		return nil, errors.New("pending redis store: client is required")
	}
	if deps.KeyPrefix == "" {
		return nil, errors.New("pending redis store: key prefix is required")
	}
	return &RedisStore{rdb: deps.Redis, prefix: deps.KeyPrefix}, nil
}

func (s *RedisStore) scheduleKey(chain string) string { return s.prefix + chain }
func (s *RedisStore) entriesKey(chain string) string  { return s.prefix + chain + ":entries" }

func (s *RedisStore) Put(ctx context.Context, entry depositpending.Entry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode pending entry: %w", err)
	}
	member := strconv.FormatUint(entry.Block, 10)
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, s.entriesKey(entry.Chain), member, encoded)
	pipe.ZAdd(ctx, s.scheduleKey(entry.Chain), redis.Z{Score: float64(entry.NextRetryAt.UnixMilli()), Member: member})
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis put pending %s block %d: %w", entry.Chain, entry.Block, err)
	}
	return nil
}

func (s *RedisStore) Delete(ctx context.Context, chain string, block uint64) error {
	member := strconv.FormatUint(block, 10)
	pipe := s.rdb.TxPipeline()
	pipe.HDel(ctx, s.entriesKey(chain), member)
	pipe.ZRem(ctx, s.scheduleKey(chain), member)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis delete pending %s block %d: %w", chain, block, err)
	}
	return nil
}

func (s *RedisStore) List(ctx context.Context, chain string) ([]depositpending.Entry, error) {
	raw, err := s.rdb.HGetAll(ctx, s.entriesKey(chain)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis list pending %s: %w", chain, err)
	}
	entries := make([]depositpending.Entry, 0, len(raw))
	for member, value := range raw {
		var entry depositpending.Entry
		if err := json.Unmarshal([]byte(value), &entry); err != nil {
			return nil, fmt.Errorf("redis pending %s block %s is corrupt: %w", chain, member, err)
		}
		entries = append(entries, entry)
	}
	sortByBlock(entries)
	return entries, nil
}

func sortByBlock(entries []depositpending.Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Block < entries[j].Block })
}
