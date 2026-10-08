package facades

import (
	"errors"

	redisfacades "github.com/goravel/redis/facades"
	goredis "github.com/redis/go-redis/v9"
)

// redisConnection is the Goravel Redis connection the cache and the app share
// (REDIS_HOST, REDIS_PORT, REDIS_PASSWORD, REDIS_DB in config/database.go).
const redisConnection = "default"

// Redis returns the client of the default Goravel Redis connection, the one the
// cache uses. goravel/redis reports an unreachable server as a nil client with
// a nil error, which is turned into an error here so no caller runs without Redis.
func Redis() (goredis.UniversalClient, error) {
	client, err := redisfacades.Instance(redisConnection)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("redis is unreachable on the default connection (check REDIS_HOST, REDIS_PORT, REDIS_PASSWORD, REDIS_DB)")
	}
	return client, nil
}
