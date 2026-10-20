package providers

import (
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/deposit"
)

type intConfig map[string]int

func (c intConfig) GetInt(path string, defaultValue ...int) int {
	if value, ok := c[path]; ok {
		return value
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return 0
}

func TestChain_ScanOverrides_ReadTheChainEntryOfDepositScan(t *testing.T) {
	read := chainScanOverrides(intConfig{
		"vault.deposit_scan.chains.sol.concurrency":     1,
		"vault.deposit_scan.chains.sol.batch_blocks":    20,
		"vault.deposit_scan.chains.sol.catch_up_blocks": 60,
		"vault.deposit_scan.chains.bsc.concurrency":     2,
		"vault.deposit_scan.concurrency":                8,
	})

	if got := read("sol"); got != (deposit.ChainScanOverride{BatchBlocks: 20, CatchUpBlocks: 60, Concurrency: 1}) {
		t.Fatalf("sol override %+v", got)
	}
	if got := read("bsc"); got != (deposit.ChainScanOverride{Concurrency: 2}) {
		t.Fatalf("bsc override %+v", got)
	}
	if got := read("eth"); got != (deposit.ChainScanOverride{}) {
		t.Fatalf("a chain without its own entry keeps the global window, got %+v", got)
	}
}

func TestScan_AlertThresholds_ReadTheGlobalAndTheChainEntries(t *testing.T) {
	cfg := intConfig{
		"vault.deposit_scan.alert_lag_blocks":               200,
		"vault.deposit_scan.alert_stale_minutes":            10,
		"vault.deposit_scan.chains.btc.alert_lag_blocks":    3,
		"vault.deposit_scan.chains.btc.alert_stale_minutes": 30,
		"vault.deposit_scan.chains.sol.alert_lag_blocks":    -1,
	}

	global, perChain := scanAlertThresholds(cfg)

	if global != (deposit.ScanAlertThresholds{LagBlocks: 200, StaleAfter: 10 * time.Minute}) {
		t.Fatalf("global thresholds %+v", global)
	}
	if got := perChain("btc"); got != (deposit.ScanAlertThresholds{LagBlocks: 3, StaleAfter: 30 * time.Minute}) {
		t.Fatalf("btc thresholds %+v", got)
	}
	if got := perChain("sol"); got != (deposit.ScanAlertThresholds{}) {
		t.Fatalf("a negative value keeps the global threshold, got %+v", got)
	}
	if got := perChain("eth"); got != (deposit.ScanAlertThresholds{}) {
		t.Fatalf("a chain without its own entry keeps the global thresholds, got %+v", got)
	}
}
