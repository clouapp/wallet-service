package providers

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestResolveGasReadinessThreshold_FromChainRow(t *testing.T) {
	raw := "5000000000000000"
	ch := &models.Chain{ID: "eth", GasReadinessThresholdRaw: &raw}
	got := resolveGasReadinessThreshold(ch)
	if got == nil || got.String() != "5000000000000000" {
		t.Fatalf("expected from row, got %v", got)
	}
}

func TestResolveGasReadinessThreshold_FallbackToDefaults(t *testing.T) {
	ch := &models.Chain{ID: "eth"}
	got := resolveGasReadinessThreshold(ch)
	if got == nil || got.String() != "5000000000000000" {
		t.Fatalf("expected defaults fallback 5000000000000000, got %v", got)
	}
}

func TestResolveGasReadinessThreshold_NilForBTC(t *testing.T) {
	ch := &models.Chain{ID: "btc"}
	got := resolveGasReadinessThreshold(ch)
	if got != nil {
		t.Fatalf("expected nil for btc, got %v", got)
	}
}

func TestResolveDustThresholdNative_FromChainRow(t *testing.T) {
	raw := "500000000000000"
	ch := &models.Chain{ID: "eth", DustThresholdNativeRaw: &raw}
	got := resolveDustThresholdNative(ch)
	if got == nil || got.String() != "500000000000000" {
		t.Fatalf("expected from row, got %v", got)
	}
}

func TestResolveDustThresholdNative_FallbackToDefaults(t *testing.T) {
	ch := &models.Chain{ID: "polygon"}
	got := resolveDustThresholdNative(ch)
	if got == nil || got.String() != "100000000000000000" {
		t.Fatalf("expected polygon default 100000000000000000, got %v", got)
	}
}
