package chains

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/rpcendpoints"
)

func TestList_From_KeepsHostsAndNullsAndNeverAURL(t *testing.T) {
	cooling := time.Date(2026, 10, 19, 14, 4, 0, 0, time.FixedZone("BRT", -3*3600))
	view := rpcendpoints.View{
		ChainID: "eth", AdapterType: "evm", Network: "ethereum-sepolia", FailsOver: true,
		Endpoints: []rpcendpoints.Endpoint{
			{Position: 1, Primary: true, Host: "5.104.72.66:8545", Fingerprint: "abc", Enabled: true, Usable: true,
				Health: &chain.RPCEndpointHealth{ConsecutiveFailures: 3, CoolingDownUntil: cooling, LastError: "timeout", LastErrorAt: cooling}},
			{Position: 2, Host: "sepolia.drpc.org", Fingerprint: "def", Enabled: true, Usable: true,
				Probe: &chainregistry.EndpointProbe{Network: "ethereum-sepolia", Head: 11937030, Latency: 1500 * time.Microsecond * 100}},
			{Position: 3, Fingerprint: "", EnvVar: "ETH_FALLBACK_URL", Enabled: false, Usable: false,
				Probe: &chainregistry.EndpointProbe{Err: errors.New("rpc endpoint did not say which network it serves")}},
		},
	}

	raw, err := json.Marshal(ListFrom(view))
	if err != nil {
		t.Fatal(err)
	}
	wire := string(raw)
	for _, want := range []string{
		`"healthObservedAt":null`,
		`"coolingDownUntil":"2026-10-19T17:04:00Z"`,
		`"health":null`,
		`"probe":{"network":"ethereum-sepolia","head":11937030,"latencyMs":150}`,
		`"error":"rpc endpoint did not say which network it serves"`,
		`"envVar":"ETH_FALLBACK_URL"`,
	} {
		if !strings.Contains(wire, want) {
			t.Fatalf("wire lacks %s:\n%s", want, wire)
		}
	}
	if strings.Contains(wire, "://") {
		t.Fatalf("wire carries a URL: %s", wire)
	}
}

func TestChange_From_AnswersTheListAfterTheChange(t *testing.T) {
	plan := rpcendpoints.Plan{
		Before:   rpcendpoints.View{ChainID: "eth", Endpoints: []rpcendpoints.Endpoint{{Position: 1, Host: "old"}}},
		After:    rpcendpoints.View{ChainID: "eth", Endpoints: []rpcendpoints.Endpoint{{Position: 1, Host: "old"}, {Position: 2, Host: "new"}}},
		Changed:  true,
		Reloaded: true,
	}
	got := ChangeFrom(plan)
	if len(got.Endpoints) != 2 || !got.Changed || !got.Reloaded {
		t.Fatalf("change = %+v", got)
	}
	plan.Changed, plan.Reloaded = false, false
	if got := ChangeFrom(plan); len(got.Endpoints) != 1 || got.Changed {
		t.Fatalf("an unchanged list answers the list as it is: %+v", got)
	}
}
