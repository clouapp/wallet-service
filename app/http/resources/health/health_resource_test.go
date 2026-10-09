package health

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/services/deposit"
)

func TestNewHealth_Keeps_TheHealthWire(t *testing.T) {
	for name, tc := range map[string]struct {
		pending deposit.PendingHealth
		want    string
	}{
		"nothing pending": {
			deposit.PendingHealth{Counts: map[string]int{"eth": 0}},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"ok","pending":{"eth":0},"pending_total":0}}`,
		},
		"blocks waiting": {
			deposit.PendingHealth{Counts: map[string]int{"eth": 2}, Total: 2},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"pending_blocks","pending":{"eth":2},"pending_total":2}}`,
		},
		"store unreadable": {
			deposit.PendingHealth{Err: errors.New("redis down at 10.0.0.1")},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"pending_store_unavailable","pending":null,"pending_total":0,"error":"pending store unavailable"}}`,
		},
		"partly unreadable keeps the counts": {
			deposit.PendingHealth{Counts: map[string]int{"btc": 1}, Total: 1, Err: errors.New("eth down")},
			`{"status":"ok","version":"0.1.0","deposit_scanner":{"status":"pending_store_unavailable","pending":{"btc":1},"pending_total":1,"error":"pending store unavailable"}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(NewHealth(tc.pending))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Fatalf("wire = %s", raw)
			}
		})
	}
}
