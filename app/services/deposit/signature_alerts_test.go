package deposit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/tests/mocks"
)

// signatureAlertService is a deposit service over eth, btc, sol and tron, every one
// of them in signature mode since an hour, syncing every 10 minutes, with its alerts
// remembered.
func signatureAlertService(activity *memoryActivity, clock *testClock) *Service {
	svc := activityService(activity, nil, clock)
	svc.registry.RegisterChain(mocks.NewMockChain("tron"))
	svc.txRepo = &sweepConfirmMemory{}
	svc.SetScanAlertState(newMemoryAlerts(clock))
	svc.signatureSettings = func(context.Context) SignatureSyncSettings { return SignatureSyncSettings{Interval: 10 * time.Minute} }
	for _, chainID := range []string{"eth", "btc", "sol", "tron"} {
		activity.modes[chainID] = ScanModeRecord{Mode: ScanModeSignatures, Since: clock.Now().Add(-time.Hour), At: clock.Now()}
	}
	return svc
}

func TestSignature_LagThreshold_IsTheIndexMarginPlusAFewIntervalsOfSlots(t *testing.T) {
	assert.Equal(t, uint64(6000), signatureLagThreshold(10*time.Minute), "1500 + 3 × 1500 slots of 400 ms")
	assert.Equal(t, uint64(1500+3*150), signatureLagThreshold(time.Minute))
	assert.Equal(t, uint64(signatureIndexMargin), signatureLagThreshold(0), "no interval: the margin alone")
}

// An account that never settles keeps the covered slot back (a growing lag behind the
// head each cycle reads before listing the addresses) or, when no cycle ever read
// every account, stays backfilling: past the index margin plus three intervals of
// slots, or three intervals of backfilling, the tracker warns. A value equal to its
// threshold is no alert, and the alert clears once every account is read up to the
// head again, or once the block scan reads the chain.
func TestScan_Alerts_WarnWhenAnAccountOfAChainInSignatureModeNeverSettles(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	synced := activityNow.Add(-time.Minute)
	// eth trails by exactly the threshold, btc by one slot more.
	activity.signatures["eth"] = SignatureSyncRecord{Accounts: 36, ListedHead: 1_006_000, Head: 1_006_200, CoveredHead: 1_000_000, At: synced}
	activity.signatures["btc"] = SignatureSyncRecord{Accounts: 36, BackfillingAccounts: 1, ListedHead: 1_006_001, Head: 1_006_200, CoveredHead: 1_000_000,
		BackfillingSince: activityNow.Add(-5 * time.Minute), At: synced}
	// sol never read every account and has been backfilling for 31 minutes; tron for
	// exactly 30.
	activity.signatures["sol"] = SignatureSyncRecord{Accounts: 36, BackfillingAccounts: 2, Head: 1_000_000,
		BackfillingSince: activityNow.Add(-31 * time.Minute), At: synced}
	activity.signatures["tron"] = SignatureSyncRecord{Accounts: 36, BackfillingAccounts: 2, Head: 1_000_000,
		BackfillingSince: activityNow.Add(-30 * time.Minute), At: synced}
	svc := signatureAlertService(activity, clock)

	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))

	unsettled := warnings(logs(), signatureUnsettledMessage)
	require.Len(t, unsettled, 2, "btc past the lag threshold and sol past the backfilling one: %v", unsettled)
	assert.Equal(t, float64(6001), unsettled["btc"]["lag_blocks"])
	assert.Equal(t, float64(6000), unsettled["btc"]["threshold_blocks"])
	assert.Equal(t, float64(1_000_000), unsettled["btc"]["covered_slot"])
	assert.Equal(t, float64(1_006_001), unsettled["btc"]["listed_head"])
	assert.Equal(t, float64(2), unsettled["sol"]["backfilling_accounts"])
	assert.Equal(t, float64(31), unsettled["sol"]["minutes_backfilling"])
	assert.Equal(t, float64(30), unsettled["sol"]["threshold_minutes"])
	assert.Equal(t, false, unsettled["sol"]["reminder"])
	assert.Empty(t, warnings(logs(), signatureStaleMessage), "every chain completed a cycle a minute ago")

	// btc reads every account again; sol is read by the block scan again; tron's
	// accounts are still backfilling for exactly 30 minutes, as of its next cycle.
	clock.advance(time.Minute)
	activity.signatures["tron"] = SignatureSyncRecord{Accounts: 36, BackfillingAccounts: 1, Head: 1_000_150,
		BackfillingSince: clock.Now().Add(-30 * time.Minute), At: clock.Now()}
	activity.signatures["btc"] = SignatureSyncRecord{Accounts: 36, ListedHead: 1_007_600, Head: 1_007_800, CoveredHead: 1_006_100, ReadEveryAccount: true, At: clock.Now()}
	activity.modes["sol"] = ScanModeRecord{Mode: ScanModeBlocks, Since: clock.Now(), At: clock.Now()}
	activity.scans["sol"] = ScanRecord{Head: 1_000_100, Checkpoint: 1_000_100, At: clock.Now()}
	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))
	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))

	cleared := recordsOf(logs(), "INFO", signatureSettledMessage)
	require.Len(t, cleared, 2, "one line per chain when its alert clears: %v", cleared)
	byChain := map[string]map[string]any{}
	for _, record := range cleared {
		byChain[record["chain"].(string)] = record
	}
	assert.Equal(t, float64(1500), byChain["btc"]["lag_blocks"])
	assert.Equal(t, "the block scan reads the chain", byChain["sol"]["reason"])
	assert.Len(t, recordsOf(logs(), "WARN", signatureUnsettledMessage), 2, "no warning after the alerts cleared")
}

// The start of a run of cycles that leave accounts backfilling is carried from record
// to record, and a cycle that leaves none ends the run.
func TestRecord_SignatureSync_CarriesWhenAccountsStartedBackfilling(t *testing.T) {
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	activity.modes["sol"] = ScanModeRecord{Mode: ScanModeSignatures, Since: activityNow.Add(-time.Hour), At: activityNow}
	svc := activityService(activity, nil, clock)
	record := func(summary SignatureSyncSummary) SignatureSyncRecord {
		t.Helper()
		svc.recordSignatureSync(context.Background(), "sol", summary)
		got, ok := activity.syncOf("sol")
		require.True(t, ok)
		clock.advance(10 * time.Minute)
		return got
	}

	first := record(SignatureSyncSummary{Accounts: 3, BackfillingAccounts: 1, Head: 9000})
	assert.Equal(t, activityNow, first.BackfillingSince, "the first cycle that leaves an account backfilling")
	second := record(SignatureSyncSummary{Accounts: 3, BackfillingAccounts: 2, Head: 10_500})
	assert.Equal(t, activityNow, second.BackfillingSince, "carried while accounts stay backfilling")
	failedOnly := record(SignatureSyncSummary{Accounts: 3, FailedAccounts: 1, Head: 12_000})
	assert.True(t, failedOnly.BackfillingSince.IsZero(), "a cycle that leaves none backfilling ends the run")
	again := record(SignatureSyncSummary{Accounts: 3, BackfillingAccounts: 1, Head: 13_500})
	assert.Equal(t, activityNow.Add(30*time.Minute), again.BackfillingSince, "a new run starts at its first cycle")
	covered := record(SignatureSyncSummary{Accounts: 3, Head: 15_000, CoveredHead: 13_500})
	assert.True(t, covered.BackfillingSince.IsZero())
	assert.True(t, covered.ReadEveryAccount)

	activity.signatures["sol"] = SignatureSyncRecord{Accounts: 3, BackfillingAccounts: 1, BackfillingSince: activityNow, At: activityNow}
	activity.readErr = errors.New("dial tcp 10.0.0.3:6379: i/o timeout")
	unread := record(SignatureSyncSummary{Accounts: 3, BackfillingAccounts: 1, Head: 16_500})
	assert.Equal(t, activityNow.Add(50*time.Minute), unread.BackfillingSince, "a previous record that cannot be read starts the run again")
}

// End to end: an endpoint without the old ledger never proves the histories read
// down to their floor, so the accounts stay backfilling cycle after cycle. The
// tracker stays quiet for three intervals, then warns; once an endpoint proves the
// histories, every account is read up to the head and the alert clears.
func TestSync_Signatures_AnAccountThatNeverSettlesRaisesTheAlertAfterAFewIntervals(t *testing.T) {
	logs := captureLogs(t)
	f := newSignatureFixture(t)
	f.settings.Interval = 10 * time.Minute
	clock := &testClock{now: activityNow}
	f.svc.now = clock.Now
	activity := newMemoryActivity()
	activity.modes[sigChain] = ScanModeRecord{Mode: ScanModeSignatures, Since: activityNow, At: activityNow}
	f.svc.SetScanActivityStore(activity)
	f.svc.SetScanAlertState(newMemoryAlerts(clock))
	f.chain.history(sigAlice, f.chain.tx("sig-alice-1", 3000, lamports(sigAlice, 5_000, 1)))
	f.chain.hideBelow = 2000 // above the floor, the scan checkpoint sigCheckpoint
	cycle := func() SignatureSyncSummary {
		t.Helper()
		summary := f.sync(t)
		require.NoError(t, f.svc.RunWithdrawalConfirmationCheck(context.Background()))
		clock.advance(f.settings.Interval)
		return summary
	}

	for i := 0; i < 4; i++ {
		summary := cycle()
		require.Equal(t, 3, summary.BackfillingAccounts, "cycle %d: no account is proven read down to its floor", i)
		require.Empty(t, warnings(logs(), signatureUnsettledMessage), "cycle %d: within three intervals of backfilling", i)
	}
	cycle()
	unsettled := warnings(logs(), signatureUnsettledMessage)
	require.Contains(t, unsettled, sigChain, "backfilling for four intervals")
	assert.Equal(t, float64(3), unsettled[sigChain]["backfilling_accounts"])
	assert.Equal(t, float64(40), unsettled[sigChain]["minutes_backfilling"])

	f.chain.hideBelow = 0
	cycle()
	summary := cycle()
	require.Equal(t, uint64(sigCovered), summary.CoveredHead, "the endpoint showed the end of every history twice")
	assert.Len(t, recordsOf(logs(), "INFO", signatureSettledMessage), 1)
	assert.Len(t, recordsOf(logs(), "WARN", signatureUnsettledMessage), 1, "warned once, cleared once")
}

// sol left signature mode two days ago with accounts still backfilling and a covered
// slot far behind, and was listed again a minute ago: no cycle of the new period has
// completed yet. That record says nothing of the accounts now, so it raises no
// unsettled, failed or skipped-mint alert; the stale check counts from the mode's
// start.
func TestScan_Alerts_ARecordFromAnEarlierSignaturePeriodRaisesNoAlert(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	svc := signatureAlertService(activity, clock)
	activity.signatures["sol"] = SignatureSyncRecord{Accounts: 36, FailedAccounts: 3, BackfillingAccounts: 2, SkippedMints: []string{"mint-x"},
		ListedHead: 1_000_000, Head: 1_000_000, CoveredHead: 560_000,
		BackfillingSince: activityNow.Add(-50 * time.Hour), At: activityNow.Add(-48 * time.Hour)}
	activity.modes["sol"] = ScanModeRecord{Mode: ScanModeSignatures, Since: activityNow.Add(-time.Minute), At: activityNow}

	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))

	records := logs()
	for _, message := range []string{signatureUnsettledMessage, signatureFailedMessage, signatureMintsMessage, signatureStaleMessage} {
		assert.NotContains(t, warnings(records, message), "sol", message)
	}

	// Twenty-one minutes into the period with no cycle: stale, counted from the start.
	clock.advance(20 * time.Minute)
	activity.modes["sol"] = ScanModeRecord{Mode: ScanModeSignatures, Since: activityNow.Add(-time.Minute), At: clock.Now()}
	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))
	stale := warnings(logs(), signatureStaleMessage)["sol"]
	require.NotNil(t, stale)
	assert.Equal(t, activityNow.Add(-time.Minute).UTC().Format(time.RFC3339), stale["last_synced_at"])
}

// The first backfilling cycle of a new period starts its own run and carries no
// covered slot from a record of an earlier period: one backfilling cycle of the new
// period is no unsettled account. A record of this period is carried as before.
func TestRecord_SignatureSync_CarriesNothingFromAnEarlierSignaturePeriod(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	svc := signatureAlertService(activity, clock)
	activity.signatures["sol"] = SignatureSyncRecord{Accounts: 36, BackfillingAccounts: 2, ListedHead: 1_000_000, Head: 1_000_000, CoveredHead: 990_000,
		BackfillingSince: activityNow.Add(-50 * time.Hour), At: activityNow.Add(-48 * time.Hour)}
	activity.modes["sol"] = ScanModeRecord{Mode: ScanModeSignatures, Since: activityNow.Add(-2 * time.Minute), At: activityNow}

	svc.recordSignatureSync(context.Background(), "sol", SignatureSyncSummary{Accounts: 36, BackfillingAccounts: 1, ListedHead: 1_430_000, Head: 1_430_100})
	first, _ := activity.syncOf("sol")
	assert.Equal(t, activityNow, first.BackfillingSince, "the run starts at the first cycle of the period")
	assert.Zero(t, first.CoveredHead, "no covered slot is carried from the earlier period")
	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))
	assert.NotContains(t, warnings(logs(), signatureUnsettledMessage), "sol")

	clock.advance(10 * time.Minute)
	svc.recordSignatureSync(context.Background(), "sol", SignatureSyncSummary{Accounts: 36, BackfillingAccounts: 1, ListedHead: 1_431_500, Head: 1_431_600})
	second, _ := activity.syncOf("sol")
	assert.Equal(t, activityNow, second.BackfillingSince, "carried within the period")

	clock.advance(10 * time.Minute)
	svc.recordSignatureSync(context.Background(), "sol", SignatureSyncSummary{Accounts: 36, ListedHead: 1_433_000, Head: 1_433_100, CoveredHead: 1_431_500})
	clock.advance(10 * time.Minute)
	svc.recordSignatureSync(context.Background(), "sol", SignatureSyncSummary{Accounts: 36, FailedAccounts: 1, ListedHead: 1_434_500, Head: 1_434_600})
	fourth, _ := activity.syncOf("sol")
	assert.Equal(t, uint64(1_431_500), fourth.CoveredHead, "the covered slot of this period is carried")
}

// At the shortest interval the settings accept (15 s), a cycle that reads every
// account but takes a minute (150 slots) on a rate-limited endpoint confirms at a head
// 1650 slots past the covered slot: the lag is measured from the head read before the
// addresses were listed, which such a cycle leaves at most the 1500-slot margin
// behind, so it raises nothing. Cycles that leave accounts unread for longer than
// three intervals still do.
func TestScan_Alerts_AHealthyCycleAtTheShortestIntervalRaisesNoAlert(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	svc := signatureAlertService(activity, clock)
	svc.signatureSettings = func(context.Context) SignatureSyncSettings { return SignatureSyncSettings{Interval: 15 * time.Second} }
	activity.signatures["eth"] = SignatureSyncRecord{Accounts: 36, ListedHead: 1_001_500, Head: 1_001_650, CoveredHead: 1_000_000,
		ReadEveryAccount: true, At: activityNow.Add(-10 * time.Second)}
	// btc's last full read was several cycles ago: its covered slot is carried.
	activity.signatures["btc"] = SignatureSyncRecord{Accounts: 36, BackfillingAccounts: 1, ListedHead: 1_001_650, Head: 1_001_800,
		CoveredHead: 1_000_000, BackfillingSince: activityNow.Add(-40 * time.Second), At: activityNow.Add(-10 * time.Second)}

	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))

	unsettled := warnings(logs(), signatureUnsettledMessage)
	assert.NotContains(t, unsettled, "eth", "every account was read up to the head by the last cycle")
	require.Contains(t, unsettled, "btc", "1650 slots is past 1500 + 3 × 37 at a 15 s interval")
	assert.Equal(t, float64(signatureLagThreshold(15*time.Second)), unsettled["btc"]["threshold_blocks"])
}

// A chain whose adapter reads account states has its settled addresses read again by
// the rotation every signatureRotationEvery, so its lag is measured over that pace
// rather than over a short interval: the cycle that left btc a minute behind under a
// 15 s interval raises nothing there.
func TestScan_Alerts_AChainThatReadsAccountStatesIsMeasuredOverTheRotationPace(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	svc := signatureAlertService(activity, clock)
	svc.registry.RegisterChain(&stateChain{signatureChain: &signatureChain{MockChain: mocks.NewMockChain("btc")}})
	svc.signatureSettings = func(context.Context) SignatureSyncSettings { return SignatureSyncSettings{Interval: 15 * time.Second} }
	activity.signatures["btc"] = SignatureSyncRecord{Accounts: 36, ListedHead: 1_001_650, Head: 1_001_800,
		CoveredHead: 1_000_000, ReadEveryAccount: true, At: activityNow.Add(-10 * time.Second)}

	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))

	assert.NotContains(t, warnings(logs(), signatureUnsettledMessage), "btc")
	assert.Greater(t, signatureLagThreshold(signatureRotationPace(15*time.Second)), uint64(1_650))
}
