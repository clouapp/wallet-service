package providers

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestResolve_GasReadinessThreshold_FromChainRow(t *testing.T) {
	raw := "5000000000000000"
	ch := &models.Chain{ID: "eth", GasReadinessThresholdRaw: &raw}
	got := resolveGasReadinessThreshold(ch)
	if got == nil || got.String() != "5000000000000000" {
		t.Fatalf("expected from row, got %v", got)
	}
}

func TestResolve_GasReadinessThreshold_IgnoresEnvWhenColumnIsEmpty(t *testing.T) {
	t.Setenv("ETH_GAS_READINESS_THRESHOLD_WEI", "1")
	empty := ""
	ch := &models.Chain{ID: "eth", GasReadinessThresholdRaw: &empty}
	if got := resolveGasReadinessThreshold(ch); got != nil {
		t.Fatalf("empty column must not fall back to the environment, got %v", got)
	}
	if got := resolveGasReadinessThreshold(&models.Chain{ID: "btc"}); got != nil {
		t.Fatalf("missing column must not fall back to the environment, got %v", got)
	}
	if got := resolveGasReadinessThreshold(nil); got != nil {
		t.Fatalf("nil chain must not fall back to the environment, got %v", got)
	}
}

func TestResolve_DustThresholdNative_FromChainRow(t *testing.T) {
	raw := "500000000000000"
	ch := &models.Chain{ID: "eth", DustThresholdNativeRaw: &raw}
	got := resolveDustThresholdNative(ch)
	if got == nil || got.String() != "500000000000000" {
		t.Fatalf("expected from row, got %v", got)
	}
}

func TestResolve_DustThresholdNative_IgnoresEnvWhenColumnIsEmpty(t *testing.T) {
	t.Setenv("POLYGON_DUST_THRESHOLD_NATIVE_WEI", "1")
	empty := ""
	ch := &models.Chain{ID: "polygon", DustThresholdNativeRaw: &empty}
	if got := resolveDustThresholdNative(ch); got != nil {
		t.Fatalf("empty column must not fall back to the environment, got %v", got)
	}
	if got := resolveDustThresholdNative(&models.Chain{ID: "polygon"}); got != nil {
		t.Fatalf("missing column must not fall back to the environment, got %v", got)
	}
	if got := resolveDustThresholdNative(nil); got != nil {
		t.Fatalf("nil chain must not fall back to the environment, got %v", got)
	}
}
