package scanactivity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
)

func TestNew_Returns_NilForANilClientThatRecordsAndReadsNothing(t *testing.T) {
	store := New(nil)
	if store != nil {
		t.Fatal("expected a nil store without Redis")
	}
	ctx := context.Background()
	if err := store.RecordScan(ctx, "eth", deposit.ScanRecord{Head: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPush(ctx, "eth", time.Now()); err != nil {
		t.Fatal(err)
	}
	if activity, err := store.ReadActivity(ctx, []string{"eth"}); err != nil || len(activity) != 0 {
		t.Fatalf("activity %+v, err %v", activity, err)
	}
}

// testChains are chain ids unique to the test, removed with their keys afterwards.
func testChains(t *testing.T, names ...string) []string {
	t.Helper()
	client := testenv.TestRedis(t)
	base := strings.TrimSuffix(testenv.TestRedisPrefix(t, client), ":")
	chains := make([]string, len(names))
	for i, name := range names {
		chains[i] = base + "-" + name
	}
	t.Cleanup(func() {
		for _, chainID := range chains {
			_ = client.Del(context.Background(), ScanKeyPrefix+chainID, PushKeyPrefix+chainID).Err()
		}
	})
	return chains
}

func TestRecord_ScanAndPush_ReadBackPerChainWithATTL(t *testing.T) {
	client := testenv.TestRedis(t)
	chains := testChains(t, "eth", "btc", "sol")
	eth, btc, sol := chains[0], chains[1], chains[2]
	store := New(client)
	ctx := context.Background()
	scannedAt := time.Date(2026, 10, 20, 9, 30, 0, 123, time.FixedZone("CEST", 2*3600))
	pushedAt := time.Date(2026, 10, 20, 7, 31, 5, 0, time.UTC)

	if err := store.RecordScan(ctx, eth, deposit.ScanRecord{Head: 3000, Checkpoint: 2990, At: scannedAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordScan(ctx, eth, deposit.ScanRecord{Head: 3001, Checkpoint: 3001, At: scannedAt.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPush(ctx, eth, pushedAt); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPush(ctx, btc, pushedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	activity, err := store.ReadActivity(ctx, []string{eth, btc, sol})
	if err != nil {
		t.Fatal(err)
	}
	if len(activity) != 2 {
		t.Fatalf("activity %+v, want eth and btc only", activity)
	}
	ethActivity := activity[eth]
	if ethActivity.Scan == nil || ethActivity.Scan.Head != 3001 || ethActivity.Scan.Checkpoint != 3001 ||
		!ethActivity.Scan.At.Equal(scannedAt.Add(time.Second)) || ethActivity.Scan.At.Location() != time.UTC || !ethActivity.PushedAt.Equal(pushedAt) {
		t.Fatalf("eth %+v (scan %+v), want the last cycle in UTC and the push", ethActivity, ethActivity.Scan)
	}
	if btcActivity := activity[btc]; btcActivity.Scan != nil || !btcActivity.PushedAt.Equal(pushedAt.Add(time.Minute)) {
		t.Fatalf("btc %+v, want its push only", btcActivity)
	}
	for _, key := range []string{ScanKeyPrefix + eth, PushKeyPrefix + eth, PushKeyPrefix + btc} {
		ttl, err := client.TTL(ctx, key).Result()
		if err != nil || ttl <= recordTTL-time.Minute || ttl > recordTTL {
			t.Fatalf("%s ttl %s (err %v), want about %s", key, ttl, err, recordTTL)
		}
	}
}

func TestRead_Activity_ReportsAnUnreadableRecordWithoutHidingTheOthers(t *testing.T) {
	client := testenv.TestRedis(t)
	chains := testChains(t, "eth", "btc")
	eth, btc := chains[0], chains[1]
	store := New(client)
	ctx := context.Background()
	if err := client.Set(ctx, ScanKeyPrefix+eth, "{not json", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPush(ctx, btc, time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	activity, err := store.ReadActivity(ctx, []string{eth, btc})
	if err == nil || !strings.Contains(err.Error(), eth) {
		t.Fatalf("err = %v, want the chain whose record is unreadable", err)
	}
	if _, found := activity[eth]; found || activity[btc].PushedAt.IsZero() {
		t.Fatalf("activity %+v, want btc kept", activity)
	}
}

func TestRead_Activity_OfNoChainAsksNothing(t *testing.T) {
	activity, err := New(testenv.TestRedis(t)).ReadActivity(context.Background(), nil)
	if err != nil || len(activity) != 0 {
		t.Fatalf("activity %+v, err %v", activity, err)
	}
}

func TestAlert_State_RaisesRemindsAndClearsAcrossPasses(t *testing.T) {
	client := testenv.TestRedis(t)
	key := strings.TrimSuffix(testenv.TestRedisPrefix(t, client), ":") + "-scan-lag:eth"
	t.Cleanup(func() {
		_ = client.Del(context.Background(), AlertKeyPrefix+key, AlertKeyPrefix+key+reminderSuffix).Err()
	})
	store := New(client)
	ctx := context.Background()

	for i, want := range []deposit.AlertTransition{deposit.AlertRaised, deposit.AlertQuiet} {
		got, err := store.RaiseAlert(ctx, key, time.Minute)
		if err != nil || got != want {
			t.Fatalf("pass %d: %v (err %v), want %v", i, got, err, want)
		}
	}
	if err := client.Del(ctx, AlertKeyPrefix+key+reminderSuffix).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RaiseAlert(ctx, key, time.Minute); err != nil || got != deposit.AlertReminded {
		t.Fatalf("after the reminder expired: %v (err %v)", got, err)
	}
	if ttl, err := client.TTL(ctx, AlertKeyPrefix+key+reminderSuffix).Result(); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("reminder ttl %s (err %v)", ttl, err)
	}

	if was, err := store.ClearAlert(ctx, key); err != nil || !was {
		t.Fatalf("clear: %v (err %v), want it was raised", was, err)
	}
	if was, err := store.ClearAlert(ctx, key); err != nil || was {
		t.Fatalf("second clear: %v (err %v), want nothing raised", was, err)
	}
	if got, err := store.RaiseAlert(ctx, key, time.Minute); err != nil || got != deposit.AlertRaised {
		t.Fatalf("raised again after it cleared: %v (err %v)", got, err)
	}
}
