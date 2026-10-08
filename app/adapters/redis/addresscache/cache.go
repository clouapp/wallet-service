package addresscache

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/wallet"
)

// Cache adds watched addresses with SADD. The service keeps the key. Members are not logged.
type Cache struct {
	client *redis.Client
}

var _ wallet.AddressCache = (*Cache)(nil)

// New wraps client. A nil client returns a nil cache so the service keeps its nil-client path.
func New(client *redis.Client) wallet.AddressCache {
	if client == nil {
		return nil
	}
	return &Cache{client: client}
}

// SAdd adds members to key. The Redis error is returned unchanged.
func (c *Cache) SAdd(ctx context.Context, key string, members ...any) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis address cache: client is nil")
	}
	if ctx == nil {
		return fmt.Errorf("redis address cache: context is nil")
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("redis address cache: key is required")
	}
	if len(members) == 0 {
		return fmt.Errorf("redis address cache: member is required")
	}
	return c.client.SAdd(ctx, key, members...).Err()
}
