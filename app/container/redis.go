package container

import "github.com/redis/go-redis/v9"

// SharedRedis is the process Redis client. Client is nil when vault.redis_url
// is empty; callers already skip rate limits in that case.
type SharedRedis struct {
	Client *redis.Client
}
