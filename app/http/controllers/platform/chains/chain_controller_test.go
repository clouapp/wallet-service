package chains

import (
	"testing"

	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

func TestNew_ChainController_KeepsItsDependencies(t *testing.T) {
	thresholds := &chainsvc.Thresholds{}
	rpc := &chainsvc.RPC{}
	endpoints := &chainsvc.RPCEndpoints{}

	ctrl := NewChainController(ChainControllerDeps{Thresholds: thresholds, RPC: rpc, Endpoints: endpoints})
	if ctrl == nil {
		t.Fatal("NewChainController returned nil")
	}
	if ctrl.thresholds != thresholds {
		t.Fatal("platform chains controller did not keep the thresholds service")
	}
	if ctrl.rpc != rpc {
		t.Fatal("platform chains controller did not keep the rpc service")
	}
	if ctrl.endpoints != endpoints {
		t.Fatal("platform chains controller did not keep the rpc endpoints service")
	}
}

func TestNew_ChainController_RequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		build func()
		panic string
	}{
		{
			name: "thresholds service",
			build: func() {
				NewChainController(ChainControllerDeps{RPC: &chainsvc.RPC{}, Endpoints: &chainsvc.RPCEndpoints{}})
			},
			panic: "platform chains controller: thresholds service is required",
		},
		{
			name: "rpc service",
			build: func() {
				NewChainController(ChainControllerDeps{Thresholds: &chainsvc.Thresholds{}, Endpoints: &chainsvc.RPCEndpoints{}})
			},
			panic: "platform chains controller: rpc service is required",
		},
		{
			name: "rpc endpoints service",
			build: func() {
				NewChainController(ChainControllerDeps{Thresholds: &chainsvc.Thresholds{}, RPC: &chainsvc.RPC{}})
			},
			panic: "platform chains controller: rpc endpoints service is required",
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
