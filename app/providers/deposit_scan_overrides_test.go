package providers

import (
	"testing"

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
