package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestNew_Returns_NilForANilClient(t *testing.T) {
	if New(nil) != nil {
		t.Fatal("expected a nil scanner store when Redis is not configured")
	}
}

func TestNil_Store_ReportsAMissingClient(t *testing.T) {
	var store *Store
	ctx := context.Background()
	if _, err := store.Uint64(ctx, "vault:checkpoint:eth"); err == nil {
		t.Fatal("expected error for a nil store read")
	}
	if err := store.Set(ctx, "vault:checkpoint:eth", 1, 0); err == nil {
		t.Fatal("expected error for a nil store write")
	}
	if _, err := store.SIsMember(ctx, "vault:addresses:eth", "0xabc"); err == nil {
		t.Fatal("expected error for a nil store membership read")
	}
	if _, err := store.SCard(ctx, "vault:addresses:eth"); err == nil {
		t.Fatal("expected error for a nil store cardinality read")
	}
	if _, err := store.SMIsMember(ctx, "vault:addresses:eth", "0xabc"); err == nil {
		t.Fatal("expected error for a nil store multi-membership read")
	}
	if err := store.ReplaceSet(ctx, "vault:addresses:eth", "0xabc"); err == nil {
		t.Fatal("expected error for a nil store replace")
	}
}

func TestSet_And_Uint64RoundTrip(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "vault:checkpoint:eth"
	ctx := context.Background()
	store := New(client)

	if err := store.Set(ctx, key, 42, 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	stored, err := client.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(stored) != "42" {
		t.Fatal("SET encoding changed")
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl != -1 {
		t.Fatalf("ttl = %s, want no expiry", ttl)
	}

	got, err := store.Uint64(ctx, key)
	if err != nil {
		t.Fatalf("uint64: %v", err)
	}
	if got != 42 {
		t.Fatalf("value = %d", got)
	}
}

func TestSet_Keeps_APositiveTTL(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "vault:checkpoint:ttl"
	ctx := context.Background()

	if err := New(client).Set(ctx, key, 7, 45*time.Second); err != nil {
		t.Fatalf("set: %v", err)
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 30*time.Second || ttl > 45*time.Second {
		t.Fatalf("ttl = %s", ttl)
	}
}

func TestUint64_Missing_KeyIsRedisNil(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)

	_, err := New(client).Uint64(context.Background(), prefix+"vault:checkpoint:missing")
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("error = %v", err)
	}
}

func TestSet_Membership_Commands(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "vault:addresses:eth"
	ctx := context.Background()
	store := New(client)

	if err := client.SAdd(ctx, key, "0xaaa", "0xbbb").Err(); err != nil {
		t.Fatalf("sadd: %v", err)
	}

	member, err := store.SIsMember(ctx, key, "0xaaa")
	if err != nil {
		t.Fatalf("sismember: %v", err)
	}
	if !member {
		t.Fatal("expected 0xaaa to be a member")
	}
	missing, err := store.SIsMember(ctx, key, "0xccc")
	if err != nil {
		t.Fatalf("sismember missing: %v", err)
	}
	if missing {
		t.Fatal("expected 0xccc to be absent")
	}

	count, err := store.SCard(ctx, key)
	if err != nil {
		t.Fatalf("scard: %v", err)
	}
	if count != 2 {
		t.Fatalf("scard = %d", count)
	}

	present, err := store.SMIsMember(ctx, key, "0xbbb", "0xccc", "0xaaa")
	if err != nil {
		t.Fatalf("smismember: %v", err)
	}
	want := []bool{true, false, true}
	if len(present) != len(want) {
		t.Fatalf("smismember len = %d", len(present))
	}
	for i := range want {
		if present[i] != want[i] {
			t.Fatalf("smismember[%d] = %v", i, present[i])
		}
	}
}

func TestReplace_Set_SwapsMembersInOneTransaction(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "vault:addresses:eth"
	ctx := context.Background()
	if err := client.SAdd(ctx, key, "0xstale").Err(); err != nil {
		t.Fatalf("sadd: %v", err)
	}
	hook := &pipelineHook{}
	client.AddHook(hook)

	if err := New(client).ReplaceSet(ctx, key, "0xaaa", "0xbbb"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(hook.pipelines) != 1 {
		t.Fatalf("pipelines = %d", len(hook.pipelines))
	}
	got := hook.pipelines[0]
	want := []string{"multi", "del", "sadd", "exec"}
	if len(got) != len(want) {
		t.Fatalf("commands = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commands = %v", got)
		}
	}

	members, err := client.SMembers(ctx, key).Result()
	if err != nil {
		t.Fatalf("smembers: %v", err)
	}
	if len(members) != 2 || !sameMembers(members, []string{"0xaaa", "0xbbb"}) {
		t.Fatal("replaced set does not hold the new members")
	}
}

func TestReplace_Set_WithNoMembersOnlyDeletes(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "vault:addresses:btc"
	ctx := context.Background()
	if err := client.SAdd(ctx, key, "tb1qstale").Err(); err != nil {
		t.Fatalf("sadd: %v", err)
	}
	hook := &pipelineHook{}
	client.AddHook(hook)

	if err := New(client).ReplaceSet(ctx, key); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(hook.pipelines) != 1 {
		t.Fatalf("pipelines = %d", len(hook.pipelines))
	}
	got := hook.pipelines[0]
	want := []string{"multi", "del", "exec"}
	if len(got) != len(want) {
		t.Fatalf("commands = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commands = %v", got)
		}
	}
	count, err := client.Exists(ctx, key).Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if count != 0 {
		t.Fatal("empty replace left the key behind")
	}
}

func TestCommands_Canceled_Context(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := New(client)
	key := prefix + "vault:checkpoint:eth"
	setKey := prefix + "vault:addresses:eth"

	if _, err := store.Uint64(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("get error = %v", err)
	}
	if err := store.Set(ctx, key, 1, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("set error = %v", err)
	}
	if _, err := store.SIsMember(ctx, setKey, "0xabc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("sismember error = %v", err)
	}
	if _, err := store.SCard(ctx, setKey); !errors.Is(err, context.Canceled) {
		t.Fatalf("scard error = %v", err)
	}
	if _, err := store.SMIsMember(ctx, setKey, "0xabc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("smismember error = %v", err)
	}
	if err := store.ReplaceSet(ctx, setKey, "0xabc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("replace error = %v", err)
	}
}

func TestS_Card_MissingKeyIsZero(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)

	count, err := New(client).SCard(context.Background(), prefix+"vault:addresses:missing")
	if err != nil {
		t.Fatalf("scard: %v", err)
	}
	if count != 0 {
		t.Fatalf("scard = %d", count)
	}
}

type pipelineHook struct {
	pipelines [][]string
}

func (h *pipelineHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *pipelineHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (h *pipelineHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		names := make([]string, len(cmds))
		for i, cmd := range cmds {
			names[i] = cmd.Name()
		}
		h.pipelines = append(h.pipelines, names)
		return next(ctx, cmds)
	}
}

func sameMembers(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, member := range got {
		seen[member]++
	}
	for _, member := range want {
		seen[member]--
		if seen[member] < 0 {
			return false
		}
	}
	return true
}
