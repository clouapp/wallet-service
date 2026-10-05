package pending

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	depositpending "github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/tests/testutil"
)

const testChain = "tpend"

func sampleEntry(block uint64, attempts int, at time.Time) depositpending.Entry {
	return depositpending.Entry{
		Chain: testChain, Block: block, TxHashes: []string{fmt.Sprintf("tx-%d", block)},
		ErrorClass: depositpending.ClassDatabase, LastError: "insert tx: connection refused", Attempts: attempts,
		FirstFailedAt: at, LastFailedAt: at, NextRetryAt: at.Add(30 * time.Second),
	}
}

// unreachableRedis fails every call fast, standing in for a Redis that is down.
func unreachableRedis(t *testing.T) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// brokenFileStore points at a directory that was replaced by a regular file.
func brokenFileStore(t *testing.T) *depositpending.FileStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pending")
	store, err := depositpending.NewFileStore(depositpending.FileStoreDeps{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestRedisStore_PutListDeleteWithIsolatedKeys(t *testing.T) {
	ctx := context.Background()
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	store, err := NewRedisStore(RedisStoreDeps{Redis: client, KeyPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.Put(ctx, sampleEntry(40, 1, now)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, sampleEntry(40, 3, now)); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List(ctx, testChain)
	if err != nil || len(entries) != 1 || entries[0].Attempts != 3 {
		t.Fatalf("expected one replaced entry, got %+v, %v", entries, err)
	}
	score, err := client.ZScore(ctx, prefix+testChain, "40").Result()
	if err != nil || int64(score) != entries[0].NextRetryAt.UnixMilli() {
		t.Fatalf("schedule must be scored by next retry time, got %v, %v", score, err)
	}
	if err := store.Delete(ctx, testChain, 40); err != nil {
		t.Fatal(err)
	}
	if entries, err := store.List(ctx, testChain); err != nil || len(entries) != 0 {
		t.Fatalf("expected no entries after delete, got %+v, %v", entries, err)
	}
	if card, _ := client.ZCard(ctx, prefix+testChain).Result(); card != 0 {
		t.Fatalf("schedule must be emptied too, has %d", card)
	}
}

func TestNewRedisStore_RequiresClientAndPrefix(t *testing.T) {
	if _, err := NewRedisStore(RedisStoreDeps{KeyPrefix: DefaultRedisKeyPrefix}); err == nil {
		t.Fatal("expected a nil client to be rejected")
	}
	if _, err := NewRedisStore(RedisStoreDeps{Redis: unreachableRedis(t)}); err == nil {
		t.Fatal("expected an empty prefix to be rejected")
	}
}

func TestDurableStore_RedisDownFallsBackToTheFile(t *testing.T) {
	ctx := context.Background()
	redisStore, err := NewRedisStore(RedisStoreDeps{Redis: unreachableRedis(t), KeyPrefix: "test:unreachable:"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fileStore, err := depositpending.NewFileStore(depositpending.FileStoreDeps{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	store, err := depositpending.NewDurableStore(depositpending.DurableStoreDeps{Redis: redisStore, File: fileStore})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, sampleEntry(50, 1, time.Now().UTC())); err != nil {
		t.Fatalf("one backend up must be enough: %v", err)
	}
	entries, err := store.List(ctx, testChain)
	if err != nil || len(entries) != 1 || entries[0].Block != 50 {
		t.Fatalf("expected the entry from the file, got %+v, %v", entries, err)
	}
	if err := store.Delete(ctx, testChain, 50); err != nil {
		t.Fatalf("delete with one backend up must succeed: %v", err)
	}
}

func TestDurableStore_FailsWhenEveryBackendIsDown(t *testing.T) {
	ctx := context.Background()
	redisStore, err := NewRedisStore(RedisStoreDeps{Redis: unreachableRedis(t), KeyPrefix: "test:unreachable:"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := depositpending.NewDurableStore(depositpending.DurableStoreDeps{Redis: redisStore, File: brokenFileStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, sampleEntry(60, 1, time.Now().UTC())); err == nil || !strings.Contains(err.Error(), "every backend failed") {
		t.Fatalf("expected the put to fail with both backends down, got %v", err)
	}
	if _, err := store.List(ctx, testChain); err == nil {
		t.Fatal("expected the list to fail with both backends down")
	}
}

func TestDurableStore_MergesBothBackendsKeepingTheNewestEntry(t *testing.T) {
	ctx := context.Background()
	client := testutil.TestRedis(t)
	redisStore, err := NewRedisStore(RedisStoreDeps{Redis: client, KeyPrefix: testutil.TestRedisPrefix(t, client)})
	if err != nil {
		t.Fatal(err)
	}
	fileStore, err := depositpending.NewFileStore(depositpending.FileStoreDeps{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := redisStore.Put(ctx, sampleEntry(70, 1, now)); err != nil {
		t.Fatal(err)
	}
	if err := fileStore.Put(ctx, sampleEntry(70, 2, now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if err := fileStore.Put(ctx, sampleEntry(71, 1, now)); err != nil {
		t.Fatal(err)
	}
	store, err := depositpending.NewDurableStore(depositpending.DurableStoreDeps{Redis: redisStore, File: fileStore})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.List(ctx, testChain)
	if err != nil || len(entries) != 2 || entries[0].Block != 70 || entries[0].Attempts != 2 || entries[1].Block != 71 {
		t.Fatalf("expected the union with the newest entry per block, got %+v, %v", entries, err)
	}
}
