package deposit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/tests/mocks"
)

// memoryActivity is a ScanActivityStore in memory; recordErr fails every record and
// readErr every read.
type memoryActivity struct {
	mu         sync.Mutex
	scans      map[string]ScanRecord
	signatures map[string]SignatureSyncRecord
	pushes     map[string]time.Time
	recordErr  error
	readErr    error
	reads      atomic.Int32
	asked      [][]string
}

func newMemoryActivity() *memoryActivity {
	return &memoryActivity{scans: map[string]ScanRecord{}, signatures: map[string]SignatureSyncRecord{}, pushes: map[string]time.Time{}}
}

func (m *memoryActivity) RecordSignatureSync(_ context.Context, chainID string, sync SignatureSyncRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recordErr != nil {
		return m.recordErr
	}
	m.signatures[chainID] = sync
	return nil
}

func (m *memoryActivity) RecordScan(_ context.Context, chainID string, scan ScanRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recordErr != nil {
		return m.recordErr
	}
	m.scans[chainID] = scan
	return nil
}

func (m *memoryActivity) ReadActivity(_ context.Context, chainIDs []string) (map[string]ChainActivity, error) {
	m.reads.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asked = append(m.asked, append([]string(nil), chainIDs...))
	if m.readErr != nil {
		return nil, m.readErr
	}
	activity := map[string]ChainActivity{}
	for _, chainID := range chainIDs {
		var found ChainActivity
		if scan, ok := m.scans[chainID]; ok {
			copied := scan
			found.Scan = &copied
		}
		if sync, ok := m.signatures[chainID]; ok {
			copied := sync
			found.Signatures = &copied
		}
		found.PushedAt = m.pushes[chainID]
		if found.Scan != nil || found.Signatures != nil || !found.PushedAt.IsZero() {
			activity[chainID] = found
		}
	}
	return activity, nil
}

func (m *memoryActivity) recorded(chainID string) (ScanRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	scan, ok := m.scans[chainID]
	return scan, ok
}

var activityNow = time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)

// withActivity records the fixture's scan cycles in memory, at activityNow.
func (f scanFixture) withActivity() (scanFixture, *memoryActivity) {
	store := newMemoryActivity()
	f.svc.SetScanActivityStore(store)
	f.svc.now = func() time.Time { return activityNow }
	return f, store
}

func TestScan_LatestBlocks_RecordsTheHeadAndTheCheckpointACompletedCycleLeft(t *testing.T) {
	f, activity := newScanFixture(t, 3000, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4}).withCheckpoint(t, 1000).withActivity()

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}

	scan, found := activity.recorded(scanTestChain)
	if !found || scan.Head != 3000 || scan.Checkpoint != 1100 || !scan.At.Equal(activityNow) || scan.LagBlocks() != 1900 {
		t.Fatalf("recorded %+v (found %v), want head 3000, checkpoint 1100 (the catch-up window), lag 1900 at %s", scan, found, activityNow)
	}
}

func TestScan_LatestBlocks_RecordsACycleThatFoundNothingNew(t *testing.T) {
	idle, idleActivity := newScanFixture(t, 1000, DefaultScanOptions()).withCheckpoint(t, 1000).withActivity()
	if err := idle.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if scan, found := idleActivity.recorded(scanTestChain); !found || scan.Head != 1000 || scan.Checkpoint != 1000 || scan.LagBlocks() != 0 {
		t.Fatalf("idle cycle recorded %+v (found %v)", scan, found)
	}
}

func TestScan_LatestBlocks_RecordsTheFirstCycleAtTheHead(t *testing.T) {
	first, firstActivity := newScanFixture(t, 4242, DefaultScanOptions()).withRedisCache(t).withActivity()
	if err := first.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if scan, found := firstActivity.recorded(scanTestChain); !found || scan.Head != 4242 || scan.Checkpoint != 4242 {
		t.Fatalf("first cycle recorded %+v (found %v), want it to start at the head", scan, found)
	}
}

func TestScan_LatestBlocks_ACheckpointWithinTheToleranceAheadOfALaggingNodeTrailsByNoBlock(t *testing.T) {
	f, activity := newScanFixture(t, 1000, DefaultScanOptions()).withCheckpoint(t, 1005).withActivity()
	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatal(err)
	}
	if scan, found := activity.recorded(scanTestChain); !found || scan.Head != 1000 || scan.Checkpoint != 1005 || scan.LagBlocks() != 0 {
		t.Fatalf("recorded %+v (found %v)", scan, found)
	}
}

func TestScan_LatestBlocks_RecordsNoCycleThatFailed(t *testing.T) {
	failed, failedActivity := newScanFixture(t, 3000, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4}).withCheckpoint(t, 1000).withActivity()
	failed.adapter.failAt[1050] = errors.New("rpc call getBlock: Post: context deadline exceeded")
	if err := failed.svc.ScanLatestBlocks(context.Background(), scanTestChain); err == nil {
		t.Fatal("expected the failed block to fail the cycle")
	}
	if scan, found := failedActivity.recorded(scanTestChain); found {
		t.Fatalf("a failed cycle recorded %+v: the scan time must age while cycles fail", scan)
	}

	ahead, aheadActivity := newScanFixture(t, 1000, DefaultScanOptions()).withCheckpoint(t, 1000+checkpointAheadTolerance+1).withActivity()
	if err := ahead.svc.ScanLatestBlocks(context.Background(), scanTestChain); !errors.Is(err, ErrCheckpointAheadOfHead) {
		t.Fatalf("err = %v, want ErrCheckpointAheadOfHead", err)
	}
	if scan, found := aheadActivity.recorded(scanTestChain); found {
		t.Fatalf("a checkpoint far ahead of the head recorded %+v", scan)
	}
}

func TestScan_LatestBlocks_ARecordThatFailsNeverFailsTheScan(t *testing.T) {
	logs := captureLogs(t)
	f, activity := newScanFixture(t, 1008, ScanOptions{BatchBlocks: 10, CatchUpBlocks: 100, Concurrency: 4}).withCheckpoint(t, 1000).withActivity()
	activity.recordErr = errors.New("dial tcp 10.0.0.3:6379: connect: connection refused")

	if err := f.svc.ScanLatestBlocks(context.Background(), scanTestChain); err != nil {
		t.Fatalf("scan failed on its activity record: %v", err)
	}
	if got := f.checkpoint(t); got != 1008 {
		t.Fatalf("checkpoint %d, want 1008", got)
	}
	warned := false
	for _, record := range logs() {
		if record["msg"] == scanActivityNotRecordedMessage && record["level"] == "WARN" && record["chain"] == scanTestChain {
			warned = true
		}
	}
	if !warned {
		t.Fatal("expected a warning about the activity record that failed")
	}
}

// activityService is a deposit service over eth, btc and sol with the given stores.
func activityService(activity ScanActivityStore, store pending.Store, clock *testClock) *Service {
	registry := chain.NewRegistry()
	for _, chainID := range []string{"eth", "btc", "sol"} {
		registry.RegisterChain(mocks.NewMockChain(chainID))
	}
	svc := &Service{registry: registry, pending: store, now: clock.Now, scan: DefaultScanOptions(), scanFallback: DefaultScanOptions()}
	if activity != nil {
		svc.SetScanActivityStore(activity)
	}
	return svc
}

func TestScanActivity_Health_ReportsTheChainsScannedOrPushedTo(t *testing.T) {
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	activity.scans["eth"] = ScanRecord{Head: 2003, Checkpoint: 2000, At: activityNow.Add(-20 * time.Second)}
	activity.pushes["btc"] = activityNow.Add(-time.Minute)
	svc := activityService(activity, nil, clock)

	health := svc.ScanActivityHealth(context.Background())

	if health.Err != nil || len(health.Chains) != 2 {
		t.Fatalf("health %+v, want eth and btc only", health)
	}
	eth := health.Chains["eth"]
	if eth.Scan == nil || eth.Scan.LagBlocks() != 3 || !eth.PushedAt.IsZero() {
		t.Fatalf("eth %+v", eth)
	}
	btc := health.Chains["btc"]
	if btc.Scan != nil || !btc.PushedAt.Equal(activityNow.Add(-time.Minute)) {
		t.Fatalf("btc %+v", btc)
	}
	if asked := activity.asked[0]; len(asked) != 3 {
		t.Fatalf("asked for %v, want every registered chain", asked)
	}
}

func TestScanActivity_Health_ReusesItsReadingAndServesTheLastOneWhenTheStoreFails(t *testing.T) {
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	activity.scans["eth"] = ScanRecord{Head: 10, Checkpoint: 9, At: activityNow}
	svc := activityService(activity, nil, clock)

	if got := svc.ScanActivityHealth(context.Background()); got.Err != nil || len(got.Chains) != 1 {
		t.Fatalf("first reading %+v", got)
	}
	activity.scans["sol"] = ScanRecord{Head: 5, Checkpoint: 5, At: activityNow}
	if got := svc.ScanActivityHealth(context.Background()); len(got.Chains) != 1 || activity.reads.Load() != 1 {
		t.Fatalf("a check within %s reads the store again: %+v after %d reads", scanActivityCacheFor, got, activity.reads.Load())
	}

	clock.advance(scanActivityCacheFor + time.Second)
	activity.readErr = errors.New("dial tcp 10.0.0.3:6379: i/o timeout")
	if got := svc.ScanActivityHealth(context.Background()); got.Err != nil || len(got.Chains) != 1 {
		t.Fatalf("a failed reading serves the last one: %+v", got)
	}

	fresh := activityService(activity, nil, clock)
	if got := fresh.ScanActivityHealth(context.Background()); got.Err == nil || len(got.Chains) != 0 {
		t.Fatalf("with no earlier reading the failure is reported: %+v", got)
	}
}

func TestScanActivity_Health_WithoutAStoreReportsNoChain(t *testing.T) {
	svc := activityService(nil, nil, &testClock{now: activityNow})
	if got := svc.ScanActivityHealth(context.Background()); got.Err != nil || got.Chains == nil || len(got.Chains) != 0 {
		t.Fatalf("health %+v, want an empty, non-nil map", got)
	}
}

// warnings returns the WARN records of msg, by chain.
func warnings(records []map[string]any, msg string) map[string]map[string]any {
	found := map[string]map[string]any{}
	for _, record := range records {
		if record["msg"] == msg && record["level"] == "WARN" {
			found[record["chain"].(string)] = record
		}
	}
	return found
}

func TestRun_ConfirmationCheck_WarnsAboutAScanThatLagsOrStalledAndAboutPendingBlocks(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	activity.scans["eth"] = ScanRecord{Head: 3000, Checkpoint: 2400, At: activityNow.Add(-15 * time.Second)}
	activity.scans["btc"] = ScanRecord{Head: 900, Checkpoint: 900, At: activityNow.Add(-11 * time.Minute)}
	// sol trails by exactly the 500-block catch-up window and ended exactly 10 minutes
	// ago: on the thresholds, not past them.
	activity.scans["sol"] = ScanRecord{Head: 800, Checkpoint: 300, At: activityNow.Add(-DefaultScanStaleAfter)}
	// tron keeps pending blocks but no scanner runs it any more: no record, no alert.
	store := listedPending{entries: map[string][]pending.Entry{"eth": {{Block: 2101}, {Block: 2102}}, "tron": {{Block: 7}}}}
	svc := activityService(activity, store, clock)
	svc.registry.RegisterChain(mocks.NewMockChain("tron"))
	svc.txRepo = &sweepConfirmMemory{}

	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}

	records := logs()
	lagging := warnings(records, scanLagMessage)
	if len(lagging) != 1 || lagging["eth"] == nil {
		t.Fatalf("lag warnings %v, want eth only (600 blocks behind, over the 500-block catch-up window; sol trails exactly 500)", lagging)
	}
	if lagging["eth"]["lag_blocks"] != float64(600) || lagging["eth"]["threshold_blocks"] != float64(DefaultScanCatchUpBlocks) {
		t.Fatalf("lag warning %v", lagging["eth"])
	}
	stalled := warnings(records, scanStaleMessage)
	if len(stalled) != 1 || stalled["btc"] == nil || stalled["btc"]["threshold_minutes"] != float64(10) || stalled["btc"]["minutes_since_scan"] != float64(11) {
		t.Fatalf("stale warnings %v, want btc only (11 minutes over the default 10)", stalled)
	}
	pendingWarned := warnings(records, pendingBlocksMessage)
	if len(pendingWarned) != 1 || pendingWarned["eth"]["pending_blocks"] != float64(2) {
		t.Fatalf("pending warnings %v, want eth with 2 blocks and nothing for tron, which no scanner records", pendingWarned)
	}
}

func TestScan_AlertThresholds_AChainThresholdWinsOverTheGlobalOne(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	activity.scans["eth"] = ScanRecord{Head: 3000, Checkpoint: 2950, At: activityNow.Add(-3 * time.Minute)}
	activity.scans["btc"] = ScanRecord{Head: 900, Checkpoint: 899, At: activityNow.Add(-time.Hour)}
	activity.scans["sol"] = ScanRecord{Head: 700, Checkpoint: 600, At: activityNow.Add(-3 * time.Minute)}
	svc := activityService(activity, nil, clock)
	svc.txRepo = &sweepConfirmMemory{}
	svc.SetScanAlertThresholds(ScanAlertThresholds{LagBlocks: 40, StaleAfter: 2 * time.Minute}, func(chainID string) ScanAlertThresholds {
		switch chainID {
		case "btc":
			return ScanAlertThresholds{StaleAfter: time.Hour}
		case "sol":
			return ScanAlertThresholds{LagBlocks: 100, StaleAfter: 5 * time.Minute}
		}
		return ScanAlertThresholds{}
	})

	if err := svc.RunWithdrawalConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}

	records := logs()
	if lagging := warnings(records, scanLagMessage); len(lagging) != 1 || lagging["eth"]["threshold_blocks"] != float64(40) {
		t.Fatalf("lag warnings %v, want eth over the global 40 blocks and sol at exactly its own 100", lagging)
	}
	if stalled := warnings(records, scanStaleMessage); len(stalled) != 1 || stalled["eth"]["threshold_minutes"] != float64(2) {
		t.Fatalf("stale warnings %v, want eth over the global 2 minutes, btc at exactly its hour and sol under its 5 minutes", stalled)
	}
}

// memoryAlerts is a ScanAlertState in memory, on the test clock.
type memoryAlerts struct {
	clock    *testClock
	raised   map[string]bool
	remindAt map[string]time.Time
}

func newMemoryAlerts(clock *testClock) *memoryAlerts {
	return &memoryAlerts{clock: clock, raised: map[string]bool{}, remindAt: map[string]time.Time{}}
}

func (m *memoryAlerts) RaiseAlert(_ context.Context, key string, remind time.Duration) (AlertTransition, error) {
	now := m.clock.Now()
	if !m.raised[key] {
		m.raised[key], m.remindAt[key] = true, now.Add(remind)
		return AlertRaised, nil
	}
	if now.Before(m.remindAt[key]) {
		return AlertQuiet, nil
	}
	m.remindAt[key] = now.Add(remind)
	return AlertReminded, nil
}

func (m *memoryAlerts) ClearAlert(_ context.Context, key string) (bool, error) {
	was := m.raised[key]
	delete(m.raised, key)
	delete(m.remindAt, key)
	return was, nil
}

// records returns the records of msg at level.
func recordsOf(records []map[string]any, level, msg string) []map[string]any {
	var found []map[string]any
	for _, record := range records {
		if record["msg"] == msg && record["level"] == level {
			found = append(found, record)
		}
	}
	return found
}

func TestScan_Alerts_WarnWhenRaisedRemindWhileTheyLastAndSayWhenTheyClear(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	activity.scans["eth"] = ScanRecord{Head: 3000, Checkpoint: 2400, At: activityNow.Add(-15 * time.Second)}
	svc := activityService(activity, listedPending{entries: map[string][]pending.Entry{"eth": {{Block: 2401}}}}, clock)
	svc.txRepo = &sweepConfirmMemory{}
	svc.SetScanAlertState(newMemoryAlerts(clock))
	pass := func() {
		t.Helper()
		if err := svc.RunConfirmationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	scan := func(checkpoint uint64) {
		activity.scans["eth"] = ScanRecord{Head: 3000, Checkpoint: checkpoint, At: clock.Now().Add(-15 * time.Second)}
	}

	pass()
	clock.advance(time.Minute)
	scan(2400)
	pass()
	if lag, pend := recordsOf(logs(), "WARN", scanLagMessage), recordsOf(logs(), "WARN", pendingBlocksMessage); len(lag) != 1 || len(pend) != 1 || lag[0]["reminder"] != false {
		t.Fatalf("lag warnings %v, pending warnings %v: one each when raised, none the next pass", lag, pend)
	}

	clock.advance(scanAlertReminder)
	scan(2400)
	pass()
	lag := recordsOf(logs(), "WARN", scanLagMessage)
	if len(lag) != 2 || lag[1]["reminder"] != true {
		t.Fatalf("lag warnings %v, want a reminder once the reminder period passed", lag)
	}

	clock.advance(time.Minute)
	scan(3000)
	svc.pending = listedPending{}
	pass()
	pass()
	if cleared := recordsOf(logs(), "INFO", scanLagClearedMessage); len(cleared) != 1 || cleared[0]["chain"] != "eth" {
		t.Fatalf("cleared %v, want one line when the lag clears", cleared)
	}
	if cleared := recordsOf(logs(), "INFO", pendingBlocksClearedMessage); len(cleared) != 1 {
		t.Fatalf("cleared %v, want one line when the pending blocks are gone", cleared)
	}
	if lag := recordsOf(logs(), "WARN", scanLagMessage); len(lag) != 2 {
		t.Fatalf("lag warnings %v after it cleared", lag)
	}
}

// failingAlerts is a ScanAlertState whose store is down.
type failingAlerts struct{}

func (failingAlerts) RaiseAlert(context.Context, string, time.Duration) (AlertTransition, error) {
	return AlertQuiet, errors.New("dial tcp 10.0.0.3:6379: connection refused")
}

func (failingAlerts) ClearAlert(context.Context, string) (bool, error) {
	return false, errors.New("dial tcp 10.0.0.3:6379: connection refused")
}

func TestScan_Alerts_AnAlertStoreThatFailsStillWarnsEveryPass(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	activity.scans["eth"] = ScanRecord{Head: 3000, Checkpoint: 2400, At: activityNow}
	svc := activityService(activity, nil, clock)
	svc.txRepo = &sweepConfirmMemory{}
	svc.SetScanAlertState(failingAlerts{})

	for i := 0; i < 2; i++ {
		if err := svc.RunConfirmationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if lag := recordsOf(logs(), "WARN", scanLagMessage); len(lag) != 2 {
		t.Fatalf("lag warnings %v, want one per pass while the alert store is down", lag)
	}
}
