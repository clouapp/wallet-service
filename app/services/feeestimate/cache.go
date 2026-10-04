package feeestimate

import (
	"context"
	"time"
)

// Cache keeps recent estimates; a miss is (nil, false, nil).
// The Redis implementation lives in app/adapters/redis/feecache.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}
