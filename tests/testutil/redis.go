package testutil

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// defaultTestRedisDialTimeout bounds every connection attempt so an offline
// Redis can never stall the test suite beyond this budget.
const defaultTestRedisDialTimeout = 500 * time.Millisecond

// TestRedis returns a Redis client connected to the dev docker-compose Redis
// instance (waas-redis on localhost:6379, override with TEST_REDIS_ADDR). If
// the connection fails the test is SKIPPED (not failed) so the suite stays
// green when Redis is offline — matching gamba's real-Redis test approach.
//
// A t.Cleanup is registered to close the client when the test finishes.
// Callers SHOULD namespace their keys with uuid.New() or TestRedisPrefix so
// state never leaks between concurrent test runs.
func TestRedis(t *testing.T) *redis.Client {
	t.Helper()

	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  defaultTestRedisDialTimeout,
		ReadTimeout:  defaultTestRedisDialTimeout,
		WriteTimeout: defaultTestRedisDialTimeout,
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestRedisDialTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("Redis not available at %s: %v — skipping Redis-backed test", addr, err)
		return nil
	}

	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestRedisPrefix returns a unique key prefix for the current test and
// registers a t.Cleanup that deletes every key under that prefix. Use this to
// isolate state across concurrent runs without requiring FLUSHDB.
func TestRedisPrefix(t *testing.T, client *redis.Client) string {
	t.Helper()

	if client == nil {
		t.Fatal("TestRedisPrefix: client must not be nil")
	}

	prefix := fmt.Sprintf("test:%s:%d:", t.Name(), time.Now().UnixNano())

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		iter := client.Scan(ctx, 0, prefix+"*", 0).Iterator()
		var keys []string
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			return
		}
		if len(keys) > 0 {
			_ = client.Del(ctx, keys...).Err()
		}
	})

	return prefix
}
