package chain

import (
	"math/big"
	"testing"
)

// TestEVMGasReadinessThreshold verifies the getter wiring. Tasks 13+ populate cfg.
func TestEVMGasReadinessThreshold(t *testing.T) {
	got := (&EVMLive{cfg: EVMConfig{GasReadinessThreshold: big.NewInt(5_000_000_000_000_000)}}).GasReadinessThreshold()
	if got == nil || got.String() != "5000000000000000" {
		t.Fatalf("expected 5000000000000000, got %v", got)
	}
	if nilGot := (&EVMLive{cfg: EVMConfig{}}).GasReadinessThreshold(); nilGot != nil {
		t.Fatalf("expected nil when unset, got %v", nilGot)
	}
}

// TestEVMDustThresholdNativeVsToken verifies native vs token asset routing.
func TestEVMDustThresholdNativeVsToken(t *testing.T) {
	adapter := &EVMLive{cfg: EVMConfig{
		NativeSymbol:        "eth",
		DustThresholdNative: big.NewInt(500_000_000_000_000),
	}}
	if got := adapter.DustThreshold("eth"); got == nil || got.String() != "500000000000000" {
		t.Fatalf("native: expected 500000000000000, got %v", got)
	}
	if got := adapter.DustThreshold("usdt"); got != nil {
		t.Fatalf("token: expected nil, got %v", got)
	}
}
