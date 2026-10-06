package seeds

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
)

const testConfirmations = 7

func testAddedSeeds() []chainSeed {
	return buildAddedEVMChainSeeds(func(string) int { return testConfirmations })
}

func TestAdded_Chains_SeedOnTheProfileNetworkWithEnvRPCReferences(t *testing.T) {
	t.Parallel()

	want := map[string]map[string]int64{
		models.ChainNetworkProfileMainnet: {
			models.ChainBase: 8453, models.ChainArbitrum: 42161, models.ChainBSC: 56,
			models.ChainTBase: 84532, models.ChainTArbitrum: 421614, models.ChainTBSC: 97,
		},
		models.ChainNetworkProfileTestnet: {
			models.ChainBase: 84532, models.ChainArbitrum: 421614, models.ChainBSC: 97,
			models.ChainTBase: 84532, models.ChainTArbitrum: 421614, models.ChainTBSC: 97,
		},
	}
	natives := map[string]string{
		models.ChainBase: "eth", models.ChainTBase: "eth", models.ChainArbitrum: "eth", models.ChainTArbitrum: "eth",
		models.ChainBSC: "bnb", models.ChainTBSC: "bnb",
	}
	for profile, networkIDs := range want {
		seeds := testAddedSeeds()
		if len(seeds) != len(networkIDs) {
			t.Fatalf("%s: %d added seeds, want %d", profile, len(seeds), len(networkIDs))
		}
		for _, seed := range seeds {
			placed, err := withProfileNetwork(seed, profile)
			if err != nil {
				t.Fatal(err)
			}
			if placed.networkID == nil || *placed.networkID != networkIDs[seed.id] {
				t.Errorf("%s/%s: network id %v, want %d", profile, seed.id, placed.networkID, networkIDs[seed.id])
			}
			wantTestnet := profile == models.ChainNetworkProfileTestnet || models.IsTestChainID(seed.id)
			if placed.isTestnet != wantTestnet {
				t.Errorf("%s/%s: is_testnet %t, want %t", profile, seed.id, placed.isTestnet, wantTestnet)
			}
			if !seed.rpcEnvReference || seed.envVar == "" {
				t.Errorf("%s: rpc_url must be an env reference, got %+v", seed.id, seed)
			}
			if seed.nativeSymbol != natives[seed.id] || seed.nativeDecimals != 18 || seed.adapterType != models.AdapterTypeEVM {
				t.Errorf("%s: native %s/%d adapter %s", seed.id, seed.nativeSymbol, seed.nativeDecimals, seed.adapterType)
			}
			if seed.requiredConfirmations != testConfirmations {
				t.Errorf("%s: confirmations %d, want the configured %d", seed.id, seed.requiredConfirmations, testConfirmations)
			}
		}
	}
}

func TestChain_Seeds_CreateEveryMainnetBeforeTheTestRecordsPointingAtIt(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, seed := range chainSeedsWith(append(testAddedSeeds(), testTronLitecoinSeeds()...)) {
		if ids[seed.id] {
			t.Fatalf("chain %s is seeded twice", seed.id)
		}
		ids[seed.id] = true
		if seed.mainnetChainID != nil && !seen[*seed.mainnetChainID] {
			t.Fatalf("%s references %s before it is created", seed.id, *seed.mainnetChainID)
		}
		seen[seed.id] = true
	}
	for _, id := range append(append([]string(nil), models.PrimaryChainIDs...), AddedChainIDs...) {
		if !ids[id] {
			t.Errorf("chain %s is not seeded", id)
		}
	}
}

func tokensByChain(t *testing.T, profile string) map[string][]tokenSeed {
	t.Helper()
	tokens, err := tokensForChains(testAddedSeeds(), profile)
	if err != nil {
		t.Fatal(err)
	}
	byChain := map[string][]tokenSeed{}
	for _, token := range tokens {
		byChain[token.chainID] = append(byChain[token.chainID], token)
	}
	return byChain
}

func TestTestnet_Profile_SeedsTestnetContractsOnTheFlippedPrimaries(t *testing.T) {
	t.Parallel()

	byChain := tokensByChain(t, models.ChainNetworkProfileTestnet)
	for chainID, contract := range map[string]string{
		models.ChainBase: models.USDCContractBaseSepolia, models.ChainTBase: models.USDCContractBaseSepolia,
		models.ChainArbitrum: models.USDCContractArbSepolia, models.ChainTArbitrum: models.USDCContractArbSepolia,
	} {
		tokens := byChain[chainID]
		if len(tokens) != 1 || tokens[0].symbol != models.SymbolUSDC || tokens[0].contractAddress != contract || tokens[0].decimals != 6 {
			t.Errorf("%s: tokens %+v, want only USDC %s with 6 decimals", chainID, tokens, contract)
		}
	}
	for _, chainID := range []string{models.ChainBSC, models.ChainTBSC} {
		if tokens := byChain[chainID]; len(tokens) != 0 {
			t.Errorf("%s: BSC testnet has no trustworthy stablecoin; got %+v", chainID, tokens)
		}
	}
}

func TestMainnet_Profile_SeedsBSCStablecoinsWith18Decimals(t *testing.T) {
	t.Parallel()

	byChain := tokensByChain(t, models.ChainNetworkProfileMainnet)
	bsc := map[string]tokenSeed{}
	for _, token := range byChain[models.ChainBSC] {
		bsc[token.symbol] = token
	}
	if bsc[models.SymbolUSDT].contractAddress != models.USDTContractBSC || bsc[models.SymbolUSDT].decimals != 18 {
		t.Errorf("BSC USDT %+v, want %s with 18 decimals", bsc[models.SymbolUSDT], models.USDTContractBSC)
	}
	if bsc[models.SymbolUSDC].contractAddress != models.USDCContractBSC || bsc[models.SymbolUSDC].decimals != 18 {
		t.Errorf("BSC USDC %+v, want %s with 18 decimals", bsc[models.SymbolUSDC], models.USDCContractBSC)
	}
	if base := byChain[models.ChainBase]; len(base) != 1 || base[0].contractAddress != models.USDCContractBase {
		t.Errorf("Base mainnet tokens %+v, want only Circle USDC", base)
	}
	if tbase := byChain[models.ChainTBase]; len(tbase) != 1 || tbase[0].contractAddress != models.USDCContractBaseSepolia {
		t.Errorf("tbase keeps Base Sepolia USDC under the mainnet profile, got %+v", tbase)
	}
	if len(byChain[models.ChainArbitrum]) != 2 {
		t.Errorf("Arbitrum One seeds USDC and USDT0, got %+v", byChain[models.ChainArbitrum])
	}
}

func TestAdded_Chain_ExplorersFollowTheNetwork(t *testing.T) {
	t.Parallel()

	resources, err := resourcesForChains(testAddedSeeds(), models.ChainNetworkProfileTestnet)
	if err != nil {
		t.Fatal(err)
	}
	explorers := map[string]string{}
	faucets := map[string]bool{}
	for _, r := range resources {
		switch r.resourceType {
		case resourceTypeExplorer:
			explorers[r.chainID] = r.url
		case resourceTypeFaucet:
			faucets[r.chainID] = true
		}
	}
	for chainID, url := range map[string]string{
		models.ChainBase: "https://base-sepolia.blockscout.com", models.ChainArbitrum: "https://arbitrum-sepolia.blockscout.com", models.ChainBSC: "https://testnet.bsctrace.com",
	} {
		if explorers[chainID] != url || !faucets[chainID] {
			t.Errorf("%s: explorer %q faucet %t, want %s and a faucet", chainID, explorers[chainID], faucets[chainID], url)
		}
	}
}
