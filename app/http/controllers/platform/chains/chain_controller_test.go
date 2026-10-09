package chains

import (
	"testing"

	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

func TestNew_ChainController_KeepsItsDependencies(t *testing.T) {
	thresholds := &chainsvc.Thresholds{}
	rpc := &chainsvc.RPC{}

	ctrl := NewChainController(thresholds, rpc)
	if ctrl == nil {
		t.Fatal("NewChainController returned nil")
	}
	if ctrl.thresholds != thresholds {
		t.Fatal("platform chains controller did not keep the thresholds service")
	}
	if ctrl.rpc != rpc {
		t.Fatal("platform chains controller did not keep the rpc service")
	}
}

func TestNew_ChainController_RequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		build func()
		panic string
	}{
		{
			name:  "thresholds service",
			build: func() { NewChainController(nil, &chainsvc.RPC{}) },
			panic: "platform chains controller: thresholds service is required",
		},
		{
			name:  "rpc service",
			build: func() { NewChainController(&chainsvc.Thresholds{}, nil) },
			panic: "platform chains controller: rpc service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			tc.build()
			t.Fatal("expected a panic")
		})
	}
}
