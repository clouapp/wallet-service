package deposit_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	redispending "github.com/macrowallets/waas/app/adapters/redis/pending"
	"github.com/macrowallets/waas/app/adapters/redis/scanactivity"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/tests/mocks"
)

// silentRedis accepts connections and never answers, like a Redis behind a
// network partition; it closes them when the test ends.
func silentRedis(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range held {
			_ = conn.Close()
		}
	})
	return listener.Addr().String()
}

func TestHealth_AStoreThatNeverAnswersCostsOneReadingBudgetThenBacksOff(t *testing.T) {
	// The framework builds its client like this: the context's deadline is not
	// applied to the socket, which waits for its own 3 s read timeout.
	client := redis.NewClient(&redis.Options{
		Addr: silentRedis(t), ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, ContextTimeoutEnabled: false,
	})
	t.Cleanup(func() { _ = client.Close() })
	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain("eth"))
	registry.RegisterChain(mocks.NewMockChain("sol"))
	svc := deposit.NewService(deposit.Deps{Registry: registry})
	pending, err := redispending.NewRedisStore(redispending.RedisStoreDeps{Redis: client, KeyPrefix: redispending.DefaultRedisKeyPrefix})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetPendingStore(pending)
	svc.SetScanActivityStore(scanactivity.New(client))

	started := time.Now()
	readings := svc.Health(context.Background())
	first := time.Since(started)
	if first > 3*time.Second {
		t.Fatalf("the check took %s: the readings run at once, each within its 2 s budget", first)
	}
	if readings.Pending.Err == nil || readings.Activity.Err == nil {
		t.Fatalf("readings %+v, want both Redis readings reported unavailable", readings)
	}

	started = time.Now()
	again := svc.Health(context.Background())
	if second := time.Since(started); second > 200*time.Millisecond {
		t.Fatalf("the next check took %s: a failed reading is not repeated during the backoff", second)
	}
	if again.Pending.Err == nil || again.Activity.Err == nil {
		t.Fatalf("readings %+v, want the failure kept during the backoff", again)
	}
}
