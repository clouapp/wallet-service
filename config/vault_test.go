package config

import "testing"

func TestDeposit_ScanChains_ReadsEachChainsOverrideByItsSuffix(t *testing.T) {
	env := map[string]int{
		"DEPOSIT_SCAN_CONCURRENCY_SOL":         1,
		"DEPOSIT_SCAN_CONCURRENCY_BSC":         2,
		"DEPOSIT_SCAN_BATCH_BLOCKS_SOL":        20,
		"DEPOSIT_SCAN_CATCH_UP_BLOCKS_TBASE":   200,
		"DEPOSIT_SCAN_ALERT_LAG_BLOCKS_BTC":    3,
		"DEPOSIT_SCAN_ALERT_STALE_MINUTES_SOL": 5,
	}
	lookup := func(key string, defaultValue int) int {
		if value, ok := env[key]; ok {
			return value
		}
		return defaultValue
	}

	doc := depositScanChainsDocument(lookup)

	for chainID, want := range map[string][3]int{
		"sol":   {20, 0, 1},
		"bsc":   {0, 0, 2},
		"tbase": {0, 200, 0},
		"eth":   {0, 0, 0},
	} {
		chain, ok := doc[chainID].(map[string]any)
		if !ok {
			t.Fatalf("%s has no override entry", chainID)
		}
		got := [3]int{chain["batch_blocks"].(int), chain["catch_up_blocks"].(int), chain["concurrency"].(int)}
		if got != want {
			t.Fatalf("%s override %v, want %v", chainID, got, want)
		}
	}
	for chainID, want := range map[string][2]int{"btc": {3, 0}, "sol": {0, 5}, "eth": {0, 0}} {
		chain := doc[chainID].(map[string]any)
		if got := [2]int{chain["alert_lag_blocks"].(int), chain["alert_stale_minutes"].(int)}; got != want {
			t.Fatalf("%s alert thresholds %v, want %v", chainID, got, want)
		}
	}
	for _, chainID := range []string{"eth", "teth", "btc", "tbtc", "polygon", "tpolygon", "sol", "tsol", "base", "tbase",
		"arbitrum", "tarbitrum", "bsc", "tbsc", "tron", "ttron", "ltc", "tltc", "xrp", "txrp"} {
		if _, ok := doc[chainID]; !ok {
			t.Errorf("chain %s has no override entry", chainID)
		}
	}
}
