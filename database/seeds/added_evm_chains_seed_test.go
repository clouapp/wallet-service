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

func TestAddedEVMChainsSeedDoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("added_evm_chains.go")
	if err != nil {
		t.Fatalf("read added evm chains seed: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"facades.Orm", "Orm()", ".Query()", ".Exec(", ".Raw("} {
		if strings.Contains(text, needle) {
			t.Fatalf("added evm chains seed still queries outside the repository (%s)", needle)
		}
	}
}

func TestSeedMissingAddedChainsInsertsOnceAndLeavesExistingRows(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, models.ChainNetworkProfileMainnet)

	dry, err := seeds.SeedMissingAddedChains(ctx, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(dry.Chains) != len(seeds.AddedEVMChainIDs) || len(dry.Skipped) != 0 || len(dry.Tokens) != 7 || len(dry.Resources) != 9 {
		t.Fatalf("dry run reported chains=%d skipped=%d tokens=%d resources=%d", len(dry.Chains), len(dry.Skipped), len(dry.Tokens), len(dry.Resources))
	}
	chains := repositories.NewChainRepository(nil)
	if existing, err := chains.FindAll(ctx); err != nil || len(existing) != 0 {
		t.Fatalf("dry run wrote %d chains (%v)", len(existing), err)
	}

	created, err := seeds.SeedMissingAddedChains(ctx, true)
	if err != nil {
		t.Fatalf("seed added chains: %v", err)
	}
	if len(created.Chains) != len(seeds.AddedEVMChainIDs) || len(created.Skipped) != 0 || len(created.Tokens) != 7 || len(created.Resources) != 9 {
		t.Fatalf("seed reported chains=%d skipped=%d tokens=%d resources=%d", len(created.Chains), len(created.Skipped), len(created.Tokens), len(created.Resources))
	}
	assertAddedEVMChains(t, ctx)

	base, err := chains.FindByID(ctx, models.ChainBase)
	if err != nil {
		t.Fatalf("find base: %v", err)
	}
	sealedBefore := base.RpcURL
	gas := "7"
	dust := "8"
	if err := chains.UpdateThresholds(ctx, models.ChainBase, models.ChainThresholdWrite{
		GasReadinessThresholdRaw: &gas,
		DustThresholdNativeRaw:   &dust,
	}); err != nil {
		t.Fatalf("drift base thresholds: %v", err)
	}

	again, err := seeds.SeedMissingAddedChains(ctx, true)
	if err != nil {
		t.Fatalf("reseed added chains: %v", err)
	}
	if len(again.Chains) != 0 || len(again.Tokens) != 0 || len(again.Resources) != 0 || len(again.Skipped) != len(seeds.AddedEVMChainIDs) {
		t.Fatalf("reseed reported chains=%d skipped=%d tokens=%d resources=%d", len(again.Chains), len(again.Skipped), len(again.Tokens), len(again.Resources))
	}

	base, err = chains.FindByID(ctx, models.ChainBase)
	if err != nil {
		t.Fatalf("find base after reseed: %v", err)
	}
	if base.RpcURL != sealedBefore {
		t.Fatal("reseed changed the sealed endpoint")
	}
	if derefString(base.GasReadinessThresholdRaw) != "7" || derefString(base.DustThresholdNativeRaw) != "8" {
		t.Fatalf("reseed rewrote thresholds on an existing chain: gas=%q dust=%q", derefString(base.GasReadinessThresholdRaw), derefString(base.DustThresholdNativeRaw))
	}
	if base.Name != "Base" || base.Status != "active" {
		t.Fatalf("reseed rewrote base identity: name=%q status=%q", base.Name, base.Status)
	}
	if tokens, err := repositories.NewTokenRepository(nil).FindByChainID(ctx, models.ChainBase); err != nil || len(tokens) != 1 {
		t.Fatalf("reseed changed base tokens: %d (%v)", len(tokens), err)
	}
	if resources, err := repositories.NewChainResourceRepository(nil).FindByChainID(ctx, models.ChainBase); err != nil || len(resources) != 1 {
		t.Fatalf("reseed changed base resources: %d (%v)", len(resources), err)
	}
}

func assertAddedEVMChains(t *testing.T, ctx context.Context) {
	t.Helper()
	chains := repositories.NewChainRepository(nil)
	tokens := repositories.NewTokenRepository(nil)
	resources := repositories.NewChainResourceRepository(nil)
	baseConfirmations := configuredConfirmations(t, models.ChainBase)
	arbitrumConfirmations := configuredConfirmations(t, models.ChainArbitrum)
	bscConfirmations := configuredConfirmations(t, models.ChainBSC)

	wantChains := []struct {
		id            string
		name          string
		native        string
		network       int64
		envVar        string
		testnet       bool
		mainnet       string
		confirmations int
		order         int
		gas           string
		dust          string
	}{
		{models.ChainBase, "Base", models.NativeETH, models.EVMNetworkIDBaseMainnet, "BASE_RPC_URL", false, "", baseConfirmations, 9, "200000000000000", "20000000000000"},
		{models.ChainArbitrum, "Arbitrum One", models.NativeETH, models.EVMNetworkIDArbitrumMainnet, "ARBITRUM_RPC_URL", false, "", arbitrumConfirmations, 11, "200000000000000", "20000000000000"},
		{models.ChainBSC, "BNB Smart Chain", models.NativeBNB, models.EVMNetworkIDBSCMainnet, "BSC_RPC_URL", false, "", bscConfirmations, 13, "500000000000000", "50000000000000"},
		{models.ChainTBase, "Base Sepolia", models.NativeETH, models.EVMNetworkIDBaseSepolia, "TBASE_RPC_URL", true, models.ChainBase, baseConfirmations, 10, "200000000000000", "20000000000000"},
		{models.ChainTArbitrum, "Arbitrum Sepolia", models.NativeETH, models.EVMNetworkIDArbitrumSepolia, "TARBITRUM_RPC_URL", true, models.ChainArbitrum, arbitrumConfirmations, 12, "200000000000000", "20000000000000"},
		{models.ChainTBSC, "BNB Smart Chain Testnet", models.NativeBNB, models.EVMNetworkIDBSCTestnet, "TBSC_RPC_URL", true, models.ChainBSC, bscConfirmations, 14, "500000000000000", "50000000000000"},
	}
	for _, row := range wantChains {
		chain, err := chains.FindByID(ctx, row.id)
		if err != nil {
			t.Fatalf("find %s: %v", row.id, err)
		}
		if chain.Name != row.name || chain.AdapterType != models.AdapterTypeEVM || chain.NativeSymbol != row.native || chain.NativeDecimals != 18 {
			t.Fatalf("chain %s identity: name=%q adapter=%q native=%q decimals=%d", row.id, chain.Name, chain.AdapterType, chain.NativeSymbol, chain.NativeDecimals)
		}
		if chain.IsTestnet != row.testnet || chain.RequiredConfirmations != row.confirmations || chain.DisplayOrder != row.order || chain.Status != "active" {
			t.Fatalf("chain %s flags: testnet=%v confirmations=%d order=%d status=%q", row.id, chain.IsTestnet, chain.RequiredConfirmations, chain.DisplayOrder, chain.Status)
		}
		if !sameOptionalInt(chain.NetworkID, row.network, true) || !sameOptionalString(chain.MainnetChainID, row.mainnet) {
			t.Fatalf("chain %s network link does not match the seed", row.id)
		}
		if derefString(chain.GasReadinessThresholdRaw) != row.gas || derefString(chain.DustThresholdNativeRaw) != row.dust {
			t.Fatalf("chain %s thresholds: gas=%q dust=%q", row.id, derefString(chain.GasReadinessThresholdRaw), derefString(chain.DustThresholdNativeRaw))
		}
		requireDustUSD(t, row.id, chain.DustThresholdUSD, "0.1")
		requireSealedEndpoint(t, chain.RpcURL, seedEndpointPlaintext(row.envVar, true))
	}

	usdc := func(chainID, contract string, decimals int) {
		t.Helper()
		found, err := tokens.FindByChainAndContract(ctx, chainID, contract)
		if err != nil {
			t.Fatalf("find %s token: %v", chainID, err)
		}
		if found.Symbol != models.SymbolUSDC || found.Decimals != decimals || found.Status != "active" {
			t.Fatalf("%s USDC symbol=%q decimals=%d status=%q", chainID, found.Symbol, found.Decimals, found.Status)
		}
	}
	usdc(models.ChainBase, models.USDCContractBase, 6)
	usdc(models.ChainTBase, models.USDCContractBaseSepolia, 6)
	usdc(models.ChainArbitrum, models.USDCContractArbitrum, 6)
	usdc(models.ChainTArbitrum, models.USDCContractArbSepolia, 6)
	usdc(models.ChainBSC, models.USDCContractBSC, 18)
	usdt, err := tokens.FindByChainAndContract(ctx, models.ChainBSC, models.USDTContractBSC)
	if err != nil || usdt.Decimals != 18 || usdt.Symbol != models.SymbolUSDT {
		t.Fatalf("BSC USDT lookup failed or has unexpected decimals")
	}
	arbitrumUSDT, err := tokens.FindByChainAndContract(ctx, models.ChainArbitrum, models.USDTContractArbitrum)
	if err != nil || arbitrumUSDT.Decimals != 6 || arbitrumUSDT.Symbol != models.SymbolUSDT {
		t.Fatalf("Arbitrum USDT lookup failed or has unexpected decimals")
	}
	if listed, err := tokens.FindByChainID(ctx, models.ChainTBSC); err != nil || len(listed) != 0 {
		t.Fatalf("BSC testnet tokens = %d (%v)", len(listed), err)
	}

	explorer, err := resources.FindByChainTypeAndName(ctx, models.ChainBase, "explorer", "Base Blockscout")
	if err != nil || explorer.URL != "https://base.blockscout.com" || explorer.Status != "active" {
		t.Fatalf("base explorer was not seeded")
	}
	faucet, err := resources.FindByChainTypeAndName(ctx, models.ChainTBase, "faucet", "Base Sepolia Faucet")
	if err != nil || faucet.Status != "active" {
		t.Fatalf("base sepolia faucet was not seeded")
	}
}

func restoreChainNetworkProfile(t *testing.T, profile string) {
	t.Helper()
	key := "vault.chains.network_profile"
	previous := facades.Config().GetString(key)
	facades.Config().Add(key, profile)
	if got := facades.Config().GetString(key); got != profile {
		t.Fatalf("chain network profile did not stick")
	}
	t.Cleanup(func() {
		facades.Config().Add(key, previous)
	})
}
