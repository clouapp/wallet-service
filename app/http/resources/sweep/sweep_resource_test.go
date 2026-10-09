package sweep_test

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/macrowallets/waas/app/http/resources/sweep"
	"github.com/macrowallets/waas/app/models"
	sweepsvc "github.com/macrowallets/waas/app/services/sweep"
)

func wire(t *testing.T, view any) string {
	t.Helper()
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestNewConsolidation_TotalsTheSweptLegs(t *testing.T) {
	decimals := 6
	result := &sweepsvc.Result{
		Sweeps: []sweepsvc.CompletedSweep{
			{From: models.Address{Address: "TChildA"}, TxHash: "a1", Amount: big.NewInt(7_000_000)},
			{From: models.Address{Address: "TChildB"}, TxHash: "b2", Amount: big.NewInt(2_500_001)},
		},
		AssetDecimals: &decimals,
		EstimatedGas:  big.NewInt(21000),
	}

	const want = `{"plan_summary":{"children_swept":2,"decimals":6,"dust_ignored":0,"estimated_gas_cost":"21000","total_amount":"9500001"},` +
		`"transactions":[{"amount":"7000000","from":"TChildA","origin":"manual_consolidation","status":"confirming","tx_hash":"a1"},` +
		`{"amount":"2500001","from":"TChildB","origin":"manual_consolidation","status":"confirming","tx_hash":"b2"}]}`
	if got := wire(t, sweep.NewConsolidation(result)); got != want {
		t.Fatalf("wire changed\n got %s\nwant %s", got, want)
	}
}

func TestNewConsolidation_WithoutLegsTotalsZeroAndOmitsWhatIsUnknown(t *testing.T) {
	const want = `{"plan_summary":{"children_swept":0,"dust_ignored":0,"estimated_gas_cost":"0","total_amount":"0"},"transactions":[]}`
	if got := wire(t, sweep.NewConsolidation(&sweepsvc.Result{})); got != want {
		t.Fatalf("wire changed\n got %s\nwant %s", got, want)
	}
}

func TestNewConsolidation_ReportsTheFailedStep(t *testing.T) {
	result := &sweepsvc.Result{FailedStep: &sweepsvc.FailedStep{Index: 2, LastError: "nonce too low", RetryReady: true}}

	const want = `{"failed_step":{"index":2,"last_error":"nonce too low","retry_ready":true},` +
		`"plan_summary":{"children_swept":0,"dust_ignored":0,"estimated_gas_cost":"0","total_amount":"0"},"transactions":[]}`
	if got := wire(t, sweep.NewConsolidation(result)); got != want {
		t.Fatalf("wire changed\n got %s\nwant %s", got, want)
	}
}

func TestNewGasStatus(t *testing.T) {
	cases := map[string]struct {
		status *sweepsvc.GasStatus
		want   string
	}{
		"unread balance and threshold are left out": {
			&sweepsvc.GasStatus{Status: "unseeded", BaseAddress: "0xbase", NativeAsset: "eth", LastCheckedAt: 1713500000},
			`{"base_address":"0xbase","gas_status":"unseeded","last_checked_at":1713500000,"native_asset":"eth"}`,
		},
		"balance and threshold show raw and display": {
			&sweepsvc.GasStatus{Status: "low", BaseAddress: "0xbase", NativeAsset: "eth", NativeBalance: big.NewInt(3), Threshold: big.NewInt(5), LastCheckedAt: 7},
			`{"base_address":"0xbase","gas_status":"low","last_checked_at":7,"native_asset":"eth",` +
				`"native_balance_display":"3","native_balance_raw":"3","threshold_display":"5","threshold_raw":"5"}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := wire(t, sweep.NewGasStatus(tc.status)); got != tc.want {
				t.Fatalf("wire changed\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestNewPreview(t *testing.T) {
	plan := &sweepsvc.Plan{
		Strategy:      sweepsvc.StrategyMultiSweep,
		ReachesTarget: true,
		Sweeps:        make([]sweepsvc.PlannedSweep, 2),
		EstimatedGas:  big.NewInt(7),
		BaseBalance:   big.NewInt(1),
		DustIgnored: []sweepsvc.AddressBalance{
			{Address: models.Address{Address: "d"}, Reason: "below_dust_threshold"},
			{Address: models.Address{Address: "e"}, Balance: big.NewInt(2), Reason: "below_dust_threshold"},
		},
	}

	const want = `{"base_balance":"1","dust_ignored":[{"address":"d","balance":"0","reason":"below_dust_threshold"},` +
		`{"address":"e","balance":"2","reason":"below_dust_threshold"}],` +
		`"estimated_gas_total_native":"7","reaches_target":true,"strategy":"multi_sweep","sweeps_required":2}`
	if got := wire(t, sweep.NewPreview(plan)); got != want {
		t.Fatalf("wire changed\n got %s\nwant %s", got, want)
	}

	const empty = `{"dust_ignored":[],"estimated_gas_total_native":"0","reaches_target":false,"strategy":"","sweeps_required":0}`
	if got := wire(t, sweep.NewPreview(&sweepsvc.Plan{})); got != empty {
		t.Fatalf("wire changed\n got %s\nwant %s", got, empty)
	}
}
