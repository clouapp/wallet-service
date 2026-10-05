// Package pendingredis opens the production pending-deposit Redis adapter for
// service tests. Those tests cannot import app/adapters.
package pendingredis

import (
	"github.com/redis/go-redis/v9"

	redispending "github.com/macrowallets/waas/app/adapters/redis/pending"
	"github.com/macrowallets/waas/app/services/deposit/pending"
)

// Open builds the Redis backend the API wires at boot.
func Open(client *redis.Client, prefix string) (pending.Store, error) {
	return redispending.NewRedisStore(redispending.RedisStoreDeps{Redis: client, KeyPrefix: prefix})
}
