package testenv

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// defaultTestRedisDialTimeout bounds every connection attempt so an offline
// Redis can never stall the test suite beyond this budget.
const defaultTestRedisDialTimeout = 500 * time.Millisecond

const (
	defaultTestRedisDatabase = 15
	defaultTestRedisHost     = "localhost"
	defaultTestRedisPort     = "6379"
)

// testRedisDatabase is REDIS_DB (set by .env.testing), defaultTestRedisDatabase when unset;
// the live index is refused.
func testRedisDatabase() (int, error) {
	raw := strings.TrimSpace(os.Getenv("REDIS_DB"))
	if raw == "" {
		return defaultTestRedisDatabase, nil
	}
	database, err := strconv.Atoi(raw)
	if err != nil || database == liveRedisDatabase {
		return 0, fmt.Errorf("REDIS_DB must be a non-live Redis index (not %d), got %q", liveRedisDatabase, raw)
	}
	return database, nil
}

// testRedisAddress is TEST_REDIS_ADDR, else localhost:$REDIS_PORT (the dev compose port).
func testRedisAddress() string {
	if addr := strings.TrimSpace(os.Getenv("TEST_REDIS_ADDR")); addr != "" {
		return addr
	}
	port := strings.TrimSpace(os.Getenv("REDIS_PORT"))
	if port == "" {
		port = defaultTestRedisPort
	}
	return net.JoinHostPort(defaultTestRedisHost, port)
}

// TestRedis returns a Redis client connected to the dev docker-compose Redis
// instance (waas-redis on localhost:$REDIS_PORT, override with TEST_REDIS_ADDR),
// on the REDIS_DB index of .env.testing, never the live index 0. If
// the connection fails the test is SKIPPED (not failed) so the suite stays
// green when Redis is offline — matching gamba's real-Redis test approach.
//
// A t.Cleanup is registered to close the client when the test finishes.
// Callers SHOULD namespace their keys with uuid.New() or TestRedisPrefix so
// state never leaks between concurrent test runs.
func TestRedis(t *testing.T) *redis.Client {
	t.Helper()

	addr := testRedisAddress()
	database, err := testRedisDatabase()
	if err != nil {
		t.Fatalf("TestRedis: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		DB:           database,
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
