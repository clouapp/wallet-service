package providers

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/config"
)

func TestGasReadinessDefaultsFrom_CopiesRawThresholds(t *testing.T) {
	got := gasReadinessDefaultsFrom(map[string]config.SweepThresholds{
		"eth": {GasReadinessRaw: "5000000000000000", DustNativeRaw: "1", DustUSD: 1},
		"btc": {GasReadinessRaw: "", DustNativeRaw: "10000"},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(got))
	}
	if got["eth"].Raw != "5000000000000000" {
		t.Fatalf("expected eth raw copied, got %q", got["eth"].Raw)
	}
	if got["btc"].Raw != "" {
		t.Fatalf("expected empty btc raw copied, got %q", got["btc"].Raw)
	}
}

func TestSweepGasDefaults_MatchConfiguredSweepDefaults(t *testing.T) {
	configured := config.SweepDefaults()
	got := sweepGasDefaults()
	if len(got) != len(configured) {
		t.Fatalf("expected %d chains, got %d", len(configured), len(got))
	}
	for chainID, thresholds := range configured {
		value, ok := got[chainID]
		if !ok {
			t.Fatalf("missing chain %s", chainID)
		}
		if value.Raw != thresholds.GasReadinessRaw {
			t.Fatalf("chain %s raw = %q, SweepDefaults = %q", chainID, value.Raw, thresholds.GasReadinessRaw)
		}
	}
}

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
