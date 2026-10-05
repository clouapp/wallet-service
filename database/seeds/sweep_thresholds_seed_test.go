package seeds_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/database/seeds"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestSweepThresholdsSeedDoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("sweep_thresholds.go")
	if err != nil {
		t.Fatalf("read sweep thresholds seed: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"facades.", "Orm()", ".Query()", ".Exec(", ".Raw("} {
		if strings.Contains(text, needle) {
			t.Fatalf("sweep thresholds seed still queries outside the repository (%s)", needle)
		}
	}
}

func TestSeedSweepThresholdsWritesTheCatalogAndRefreshesItOnRerun(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	if profile := strings.TrimSpace(facades.Config().GetString("vault.chains.network_profile")); profile != "" {
		t.Fatalf("expected an empty chain network profile, got %q", profile)
	}

	if err := seeds.SeedChains(ctx); err != nil {
		t.Fatalf("seed chains: %v", err)
	}
	if err := seeds.SeedSweepThresholds(ctx); err != nil {
		t.Fatalf("seed sweep thresholds: %v", err)
	}
	assertSweepThresholdCatalog(t, ctx)

	chains := repositories.NewChainRepository(nil)
	eth, err := chains.FindByID(ctx, models.ChainETH)
	if err != nil {
		t.Fatalf("find eth: %v", err)
	}
	nameBefore, endpointBefore := eth.Name, eth.RpcURL
	gas, dust, usd := "9", "8", "4"
	if err := chains.UpdateThresholds(ctx, models.ChainETH, models.ChainThresholdWrite{
		GasReadinessThresholdRaw: &gas,
		DustThresholdNativeRaw:   &dust,
		DustThresholdUSD:         &usd,
	}); err != nil {
		t.Fatalf("drift eth thresholds: %v", err)
	}
	btcGas := "5"
	if err := chains.UpdateThresholds(ctx, models.ChainBTC, models.ChainThresholdWrite{
		GasReadinessThresholdRaw: &btcGas,
	}); err != nil {
		t.Fatalf("drift btc gas: %v", err)
	}

	if err := seeds.SeedSweepThresholds(ctx); err != nil {
		t.Fatalf("reseed sweep thresholds: %v", err)
	}
	assertSweepThresholdCatalog(t, ctx)

	eth, err = chains.FindByID(ctx, models.ChainETH)
	if err != nil {
		t.Fatalf("find eth after reseed: %v", err)
	}
	if eth.Name != nameBefore || eth.RpcURL != endpointBefore {
		t.Fatal("reseed changed a column outside the threshold catalog")
	}
	if strings.HasPrefix(eth.RpcURL, "http://") || strings.HasPrefix(eth.RpcURL, "https://") {
		t.Fatal("reseed stored an endpoint in the clear")
	}

	if err := seeds.SeedSweepThresholds(ctx); err != nil {
		t.Fatalf("second reseed: %v", err)
	}
	assertSweepThresholdCatalog(t, ctx)
}

func assertSweepThresholdCatalog(t *testing.T, ctx context.Context) {
	t.Helper()
	want := []struct {
		id      string
		gas     string
		dust    string
		dustUSD string
	}{
		{models.ChainETH, "5000000000000000", "500000000000000", "1"},
		{models.ChainTETH, "5000000000000000", "500000000000000", "1"},
		{models.ChainPolygon, "500000000000000000", "100000000000000000", "0.1"},
		{models.ChainTPolygon, "500000000000000000", "100000000000000000", "0.1"},
		{models.ChainSOL, "10000000", "1000000", "1"},
		{models.ChainTSOL, "10000000", "1000000", "1"},
		{models.ChainBTC, "", "10000", "0"},
		{models.ChainTBTC, "", "10000", "0"},
		{models.ChainBase, "200000000000000", "20000000000000", "0.1"},
		{models.ChainTBase, "200000000000000", "20000000000000", "0.1"},
		{models.ChainArbitrum, "200000000000000", "20000000000000", "0.1"},
		{models.ChainTArbitrum, "200000000000000", "20000000000000", "0.1"},
		{models.ChainBSC, "500000000000000", "50000000000000", "0.1"},
		{models.ChainTBSC, "500000000000000", "50000000000000", "0.1"},
	}

	chains := repositories.NewChainRepository(nil)
	found, err := chains.FindAll(ctx)
	if err != nil {
		t.Fatalf("list chains: %v", err)
	}
	byID := make(map[string]models.Chain, len(found))
	for _, chain := range found {
		byID[chain.ID] = chain
	}
	if len(byID) < len(want) {
		t.Fatalf("seeded %d chains, want at least %d", len(byID), len(want))
	}
	for _, row := range want {
		chain, ok := byID[row.id]
		if !ok {
			t.Fatalf("missing seeded chain %s", row.id)
		}
		if derefString(chain.GasReadinessThresholdRaw) != row.gas || derefString(chain.DustThresholdNativeRaw) != row.dust {
			t.Fatalf("chain %s thresholds: gas=%q dust=%q", row.id, derefString(chain.GasReadinessThresholdRaw), derefString(chain.DustThresholdNativeRaw))
		}
		requireDustUSD(t, row.id, chain.DustThresholdUSD, row.dustUSD)
		if chain.DustThresholdUSD.Decimal.IsNegative() {
			t.Fatalf("chain %s stored a negative dust usd", row.id)
		}
	}
}
