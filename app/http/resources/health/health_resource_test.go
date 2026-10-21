package health

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/deposit"
)

func TestNewHealth_Keeps_TheHealthWire(t *testing.T) {
	for name, tc := range map[string]struct {
		pending deposit.PendingHealth
		want    string
	}{
		"nothing pending": {
			deposit.PendingHealth{Counts: map[string]int{"eth": 0}},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"ok","pending":{"eth":0},"pending_total":0,"chains":{}},` + settledTracker + `}`,
		},
		"blocks waiting": {
			deposit.PendingHealth{Counts: map[string]int{"eth": 2}, Total: 2},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"pending_blocks","pending":{"eth":2},"pending_total":2,"chains":{}},` + settledTracker + `}`,
		},
		"store unreadable": {
			deposit.PendingHealth{Err: errors.New("redis down at 10.0.0.1")},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"pending_store_unavailable","pending":null,"pending_total":0,"chains":{},"error":"pending store unavailable"},` + settledTracker + `}`,
		},
		"partly unreadable keeps the counts": {
			deposit.PendingHealth{Counts: map[string]int{"btc": 1}, Total: 1, Err: errors.New("eth down")},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"pending_store_unavailable","pending":{"btc":1},"pending_total":1,"chains":{},"error":"pending store unavailable"},` + settledTracker + `}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(NewHealth(tc.pending, settled, noActivity))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Fatalf("wire = %s", raw)
			}
		})
	}
}

var (
	settled    = deposit.StaleOutboundHealth{Counts: map[string]int{}, After: 6 * time.Hour, Reorged: map[string]int{}}
	noActivity = deposit.ScanActivityHealth{Chains: map[string]deposit.ChainActivity{}}
)

const settledTracker = `"confirmation_tracker":{"status":"ok","stale":{},"stale_total":0,"stale_after_hours":6,"reorg_unresolved":{},"reorg_unresolved_total":0}`

func TestNewHealth_ReportsOutboundTransfersLeftUnsettled(t *testing.T) {
	nothingPending := deposit.PendingHealth{Counts: map[string]int{}}
	for name, tc := range map[string]struct {
		stale deposit.StaleOutboundHealth
		want  string
	}{
		"transfers left unsettled": {
			deposit.StaleOutboundHealth{Counts: map[string]int{"eth": 2, "sol": 1}, Total: 3, After: 6 * time.Hour, Reorged: map[string]int{"eth": 1}, ReorgedTotal: 1},
			`"confirmation_tracker":{"status":"stale_transactions","stale":{"eth":2,"sol":1},"stale_total":3,"stale_after_hours":6,"reorg_unresolved":{"eth":1},"reorg_unresolved_total":1}`,
		},
		"reorganized deposits unresolved": {
			deposit.StaleOutboundHealth{Counts: map[string]int{}, After: 6 * time.Hour, Reorged: map[string]int{"btc": 2}, ReorgedTotal: 2},
			`"confirmation_tracker":{"status":"reorg_unresolved","stale":{},"stale_total":0,"stale_after_hours":6,"reorg_unresolved":{"btc":2},"reorg_unresolved_total":2}`,
		},
		"store unreadable": {
			deposit.StaleOutboundHealth{After: 6 * time.Hour, Err: errors.New("dial tcp 10.0.0.2:5432: connect: connection refused")},
			`"confirmation_tracker":{"status":"transactions_store_unavailable","stale":null,"stale_total":0,"stale_after_hours":6,"reorg_unresolved":null,"reorg_unresolved_total":0,"error":"transactions store unavailable"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(NewHealth(nothingPending, tc.stale, noActivity))
			if err != nil {
				t.Fatal(err)
			}
			want := `{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"ok","pending":{},"pending_total":0,"chains":{}},` + tc.want + `}`
			if string(raw) != want {
				t.Fatalf("wire = %s\nwant   %s", raw, want)
			}
		})
	}
}

func TestNewHealth_ReportsHowFarEachChainsDepositDetectionGot(t *testing.T) {
	nothingPending := deposit.PendingHealth{Counts: map[string]int{}}
	scannedAt := time.Date(2026, 10, 20, 9, 30, 0, 750_000_000, time.FixedZone("CEST", 2*3600))
	pushedAt := time.Date(2026, 10, 20, 7, 29, 12, 0, time.UTC)
	for name, tc := range map[string]struct {
		activity deposit.ScanActivityHealth
		want     string
	}{
		"scanned, pushed to, or both": {
			deposit.ScanActivityHealth{Chains: map[string]deposit.ChainActivity{
				"eth": {Scan: &deposit.ScanRecord{Head: 3000, Checkpoint: 2997, At: scannedAt}, PushedAt: pushedAt},
				"btc": {Scan: &deposit.ScanRecord{Head: 900, Checkpoint: 900, At: scannedAt}},
				"sol": {PushedAt: pushedAt},
			}},
			`"chains":{"btc":{"lag_blocks":0,"last_scanned_at":"2026-10-20T07:30:00Z"},` +
				`"eth":{"lag_blocks":3,"last_scanned_at":"2026-10-20T07:30:00Z","last_push_at":"2026-10-20T07:29:12Z"},` +
				`"sol":{"last_push_at":"2026-10-20T07:29:12Z"}}`,
		},
		"store unreadable": {
			deposit.ScanActivityHealth{Chains: map[string]deposit.ChainActivity{}, Err: errors.New("dial tcp 10.0.0.3:6379: i/o timeout")},
			`"chains":{},"chains_error":"scan activity unavailable"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(NewHealth(nothingPending, settled, tc.activity))
			if err != nil {
				t.Fatal(err)
			}
			want := `{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"ok","pending":{},"pending_total":0,` + tc.want + `},` + settledTracker + `}`
			if string(raw) != want {
				t.Fatalf("wire = %s\nwant   %s", raw, want)
			}
		})
	}
}
