package chains

import (
	"testing"

	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

func chainsControllerDeps() ChainsControllerDeps {
	return ChainsControllerDeps{
		Thresholds: &chainsvc.Thresholds{},
		RPC:        &chainsvc.RPC{},
	}
}

func TestNewChainsControllerKeepsItsDependencies(t *testing.T) {
	deps := chainsControllerDeps()
	ctrl := NewChainsController(deps)
	if ctrl == nil {
		t.Fatal("NewChainsController returned nil")
	}
	if ctrl.thresholds != deps.Thresholds {
		t.Fatal("platform chains controller did not keep the thresholds service")
	}
	if ctrl.rpc != deps.RPC {
		t.Fatal("platform chains controller did not keep the rpc service")
	}
}

func TestNewChainsControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*ChainsControllerDeps)
		panic string
	}{
		{
			name:  "thresholds service",
			clear: func(deps *ChainsControllerDeps) { deps.Thresholds = nil },
			panic: "platform chains controller: thresholds service is required",
		},
		{
			name:  "rpc service",
			clear: func(deps *ChainsControllerDeps) { deps.RPC = nil },
			panic: "platform chains controller: rpc service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := chainsControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewChainsController(deps)
			t.Fatal("expected a panic")
		})
	}
}
