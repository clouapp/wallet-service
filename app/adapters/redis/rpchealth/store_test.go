package rpchealth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
)

func TestNew_Returns_NilForANilClientThatRecordsNothing(t *testing.T) {
	store := New(nil, "api")
	if store != nil {
		t.Fatal("expected a nil store without Redis")
	}
	store.RecordRPCHealth("eth", nil)
	if _, found, err := store.RPCHealth(context.Background(), "eth"); found || err != nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestRecord_Then_ReadKeepsTheLastSnapshotInOrder(t *testing.T) {
	client := testenv.TestRedis(t)
	chainID := strings.TrimSuffix(testenv.TestRedisPrefix(t, client), ":") + "-eth"
	store := New(client, "")
	observed := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return observed }
	t.Cleanup(func() { _ = client.Del(context.Background(), keyPrefix+chainID).Err() })

	cooling := observed.Add(30 * time.Second)
	for failures := 1; failures <= 3; failures++ {
		store.RecordRPCHealth(chainID, []chain.RPCEndpointHealth{
			{Position: 1, Host: "5.104.72.66:8545", Fingerprint: "abc", ConsecutiveFailures: failures, CoolingDownUntil: cooling, LastError: "timeout", LastErrorAt: observed},
			{Position: 2, Host: "sepolia.drpc.org", Fingerprint: "def"},
		})
	}

	var snapshot chain.RPCHealthSnapshot
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, found, err := store.RPCHealth(context.Background(), chainID)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if found && got.Endpoints[0].ConsecutiveFailures == 3 {
			snapshot = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(snapshot.Endpoints) != 2 || snapshot.Process != "local" || !snapshot.ObservedAt.Equal(observed) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	primary := snapshot.Endpoints[0]
	if primary.Host != "5.104.72.66:8545" || !primary.CoolingDownUntil.Equal(cooling) || primary.LastError != "timeout" {
		t.Fatalf("primary = %+v", primary)
	}
	ttl, err := client.TTL(context.Background(), keyPrefix+chainID).Result()
	if err != nil || ttl <= time.Hour || ttl > snapshotTTL {
		t.Fatalf("ttl = %s err = %v", ttl, err)
	}
}

func TestRead_Missing_ChainIsNotFound(t *testing.T) {
	client := testenv.TestRedis(t)
	prefix := testenv.TestRedisPrefix(t, client)
	if _, found, err := New(client, "api").RPCHealth(context.Background(), prefix+"never-recorded"); found || err != nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
}
