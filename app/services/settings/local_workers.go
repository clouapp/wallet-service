package settings

import (
	"context"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/models"
)

// GroupLocalWorkers is the local workers' platform group, and KeyDepositScanChains
// its scanned chain list, which the composition root shows with its environment
// value while no row stores it (PlatformFallback).
const (
	GroupLocalWorkers    = "local_workers"
	KeyDepositScanChains = "deposit_scan_chains"
)

// scanChainOptions are the chain records a deposit scanner can read.
var scanChainOptions = []string{
	models.ChainETH, models.ChainTETH, models.ChainBTC, models.ChainTBTC,
	models.ChainPolygon, models.ChainTPolygon, models.ChainSOL, models.ChainTSOL,
	models.ChainBase, models.ChainTBase, models.ChainArbitrum, models.ChainTArbitrum,
	models.ChainBSC, models.ChainTBSC, models.ChainTron, models.ChainTTron,
	models.ChainLTC, models.ChainTLTC, models.ChainXRP, models.ChainTXRP,
}

// LocalWorkerValues is the stored local_workers row. ScanChainsStored tells a stored
// chain list (ScanChains, possibly empty: no chain is scanned) from none (the
// environment's LOCAL_DEPOSIT_SCAN_CHAINS stands).
type LocalWorkerValues struct {
	ScanChains       []string
	ScanChainsStored bool
}

func localWorkersGroup() Group {
	return Group{
		Name:    GroupLocalWorkers,
		Scope:   ScopePlatform,
		Section: sectionScanning,
		Block:   "Local workers",
		Settings: []Definition{
			{
				Key:   KeyDepositScanChains,
				Label: "Scanned chains",
				Help: "Chains whose deposits the local workers scan. A chain added starts scanning on the next tick, " +
					"a chain removed stops after its cycle in progress, no restart. " +
					"Unset keeps LOCAL_DEPOSIT_SCAN_CHAINS; an empty list scans no chain.",
				Type:    TypeStringList,
				Options: scanChainOptions,
				Default: func() any { return []string{} },
			},
		},
	}
}

// EffectiveLocalWorkers reads the platform local_workers group at the moment of use:
// a cache hit skips the database, a missing row leaves the list unstored, and a
// database failure is returned so the caller keeps the list it runs.
func (s *Service) EffectiveLocalWorkers(ctx context.Context) (LocalWorkerValues, error) {
	if s == nil {
		return LocalWorkerValues{}, errServiceRequired
	}
	if ctx == nil {
		return LocalWorkerValues{}, fmt.Errorf("local workers settings: context is required")
	}
	stored, err := s.platformValues(ctx, GroupLocalWorkers)
	if err != nil {
		return LocalWorkerValues{}, err
	}
	raw, ok := stored[KeyDepositScanChains]
	if !ok {
		return LocalWorkerValues{}, nil
	}
	return LocalWorkerValues{ScanChains: splitList(raw), ScanChainsStored: true}, nil
}

// trimmedValue is a stored value without surrounding blanks, "" when missing.
func trimmedValue(stored map[string]string, key string) string {
	return strings.TrimSpace(stored[key])
}
