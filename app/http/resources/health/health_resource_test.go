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
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"ok","pending":{"eth":0},"pending_total":0},` + settledTracker + `}`,
		},
		"blocks waiting": {
			deposit.PendingHealth{Counts: map[string]int{"eth": 2}, Total: 2},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"pending_blocks","pending":{"eth":2},"pending_total":2},` + settledTracker + `}`,
		},
		"store unreadable": {
			deposit.PendingHealth{Err: errors.New("redis down at 10.0.0.1")},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"pending_store_unavailable","pending":null,"pending_total":0,"error":"pending store unavailable"},` + settledTracker + `}`,
		},
		"partly unreadable keeps the counts": {
			deposit.PendingHealth{Counts: map[string]int{"btc": 1}, Total: 1, Err: errors.New("eth down")},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"pending_store_unavailable","pending":{"btc":1},"pending_total":1,"error":"pending store unavailable"},` + settledTracker + `}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(NewHealth(tc.pending, settled))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Fatalf("wire = %s", raw)
			}
		})
	}
}

var settled = deposit.StaleOutboundHealth{Counts: map[string]int{}, After: 6 * time.Hour}

const settledTracker = `"confirmation_tracker":{"status":"ok","stale":{},"stale_total":0,"stale_after_hours":6}`

func TestNewHealth_ReportsOutboundTransfersLeftUnsettled(t *testing.T) {
	nothingPending := deposit.PendingHealth{Counts: map[string]int{}}
	for name, tc := range map[string]struct {
		stale deposit.StaleOutboundHealth
		want  string
	}{
		"transfers left unsettled": {
			deposit.StaleOutboundHealth{Counts: map[string]int{"eth": 2, "sol": 1}, Total: 3, After: 6 * time.Hour},
			`"confirmation_tracker":{"status":"stale_transactions","stale":{"eth":2,"sol":1},"stale_total":3,"stale_after_hours":6}`,
		},
		"store unreadable": {
			deposit.StaleOutboundHealth{After: 6 * time.Hour, Err: errors.New("dial tcp 10.0.0.2:5432: connect: connection refused")},
			`"confirmation_tracker":{"status":"transactions_store_unavailable","stale":null,"stale_total":0,"stale_after_hours":6,"error":"transactions store unavailable"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(NewHealth(nothingPending, tc.stale))
			if err != nil {
				t.Fatal(err)
			}
			want := `{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"ok","pending":{},"pending_total":0},` + tc.want + `}`
			if string(raw) != want {
				t.Fatalf("wire = %s\nwant   %s", raw, want)
			}
		})
	}
}
