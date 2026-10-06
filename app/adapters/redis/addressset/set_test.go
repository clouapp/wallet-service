package addressset

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestNew_Returns_NilForANilClient(t *testing.T) {
	if New(nil) != nil {
		t.Fatal("expected a nil address set when Redis is not configured")
	}
}

func TestS_Is_MemberRejectsMissingContextAndKey(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	set := New(client)

	if _, err := set.SIsMember(nil, "vault:addresses:eth", "0xabc").Result(); err == nil {
		t.Fatal("expected error for a nil context")
	}
	if _, err := set.SIsMember(context.Background(), "  ", "0xabc").Result(); err == nil {
		t.Fatal("expected error for a blank key")
	}
}

func TestS_Is_MemberRoundTrip(t *testing.T) {
	client := testutil.TestRedis(t)
	prefix := testutil.TestRedisPrefix(t, client)
	key := prefix + "addresses"
	ctx := context.Background()
	if err := client.SAdd(ctx, key, "0xabc").Err(); err != nil {
		t.Fatalf("sadd: %v", err)
	}

	set := New(client)
	member, err := set.SIsMember(ctx, key, "0xabc").Result()
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if !member {
		t.Fatal("expected the added address to be a member")
	}

	missing, err := set.SIsMember(ctx, key, "0xother").Result()
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	if missing {
		t.Fatal("expected an unknown address to be absent")
	}
}

func TestS_Is_MemberCanceledContext(t *testing.T) {
	client := testutil.TestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New(client).SIsMember(ctx, "vault:addresses:eth", "0xabc").Result()
	if err == nil {
		t.Fatal("expected SISMEMBER to fail on a canceled context")
	}
}

func TestNil_Set_ReportsAMissingClient(t *testing.T) {
	var set *Set
	_, err := set.SIsMember(context.Background(), "vault:addresses:eth", "0xabc").Result()
	if err == nil {
		t.Fatal("expected error for a nil set")
	}
}
