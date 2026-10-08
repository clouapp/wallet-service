package seeds

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func testTronLitecoinSeeds() []chainSeed {
	return buildAddedTronLitecoinChainSeeds(func(string) int { return testConfirmations })
}

func TestTronAndLitecoinSeedOnTheProfileNetworkWithEnvRPCReferences(t *testing.T) {
	t.Parallel()

	type expected struct {
		adapterType string
		native      string
		decimals    int
		envVar      string
	}
	want := map[string]expected{
		models.ChainTron:  {models.AdapterTypeTron, models.NativeTRX, 6, "TRON_RPC_URL"},
		models.ChainTTron: {models.AdapterTypeTron, models.NativeTRX, 6, "TTRON_RPC_URL"},
		models.ChainLTC:   {models.AdapterTypeBitcoin, models.NativeLTC, 8, "LTC_RPC_URL"},
		models.ChainTLTC:  {models.AdapterTypeBitcoin, models.NativeLTC, 8, "TLTC_RPC_URL"},
	}
	networks := map[string]map[string]string{
		models.ChainNetworkProfileMainnet: {
			models.ChainTron: models.NetworkTronMainnet, models.ChainLTC: models.NetworkLitecoinMainnet,
			models.ChainTTron: models.NetworkTronNile, models.ChainTLTC: models.NetworkLitecoinTestnet,
		},
		models.ChainNetworkProfileTestnet: {
			models.ChainTron: models.NetworkTronNile, models.ChainLTC: models.NetworkLitecoinTestnet,
			models.ChainTTron: models.NetworkTronNile, models.ChainTLTC: models.NetworkLitecoinTestnet,
		},
	}
	seeds := testTronLitecoinSeeds()
	if len(seeds) != len(want) {
		t.Fatalf("%d TRON/Litecoin seeds, want %d", len(seeds), len(want))
	}
	for profile, byChain := range networks {
		for _, seed := range seeds {
			network, err := addedChainNetwork(seed, profile)
			if err != nil {
				t.Fatal(err)
			}
			if network != byChain[seed.id] {
				t.Errorf("%s/%s: network %q, want %q", profile, seed.id, network, byChain[seed.id])
			}
		}
	}
	for _, seed := range seeds {
		expect := want[seed.id]
		if seed.adapterType != expect.adapterType || seed.nativeSymbol != expect.native || seed.nativeDecimals != expect.decimals {
			t.Errorf("%s: adapter %s native %s/%d, want %+v", seed.id, seed.adapterType, seed.nativeSymbol, seed.nativeDecimals, expect)
		}
		if !seed.rpcEnvReference || seed.envVar != expect.envVar {
			t.Errorf("%s: rpc_url must reference env %s, got %+v", seed.id, expect.envVar, seed)
		}
		if seed.networkID != nil {
			t.Errorf("%s: non-EVM records carry no network id, got %d", seed.id, *seed.networkID)
		}
		if seed.requiredConfirmations != testConfirmations {
			t.Errorf("%s: confirmations %d, want the configured %d", seed.id, seed.requiredConfirmations, testConfirmations)
		}
		if models.IsTestChainID(seed.id) != seed.isTestnet {
			t.Errorf("%s: is_testnet %t disagrees with the test-record id", seed.id, seed.isTestnet)
		}
	}
}

func TestTronAndLitecoinMainnetsAreSeededBeforeTheirTestRecords(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, seed := range chainSeedsWith(testTronLitecoinSeeds()) {
		if seed.mainnetChainID != nil && !seen[*seed.mainnetChainID] {
			t.Fatalf("%s references %s before it is created", seed.id, *seed.mainnetChainID)
		}
		seen[seed.id] = true
	}
	for _, id := range AddedTronLitecoinChainIDs {
		if !seen[id] {
			t.Errorf("chain %s is not seeded", id)
		}
	}
}

func TestTronSeedsOnlyUSDTOnTheNetworkItPointsAt(t *testing.T) {
	t.Parallel()

	for profile, contract := range map[string]string{
		models.ChainNetworkProfileMainnet: models.USDTContractTron,
		models.ChainNetworkProfileTestnet: models.USDTContractTronNile,
	} {
		tokens, err := tokensForChains(testTronLitecoinSeeds(), profile)
		if err != nil {
			t.Fatal(err)
		}
		byChain := map[string][]tokenSeed{}
		for _, token := range tokens {
			byChain[token.chainID] = append(byChain[token.chainID], token)
		}
		tron := byChain[models.ChainTron]
		if len(tron) != 1 || tron[0].symbol != models.SymbolUSDT || tron[0].contractAddress != contract || tron[0].decimals != 6 {
			t.Errorf("%s tron tokens %+v, want only USDT %s with 6 decimals", profile, tron, contract)
		}
		ttron := byChain[models.ChainTTron]
		if len(ttron) != 1 || ttron[0].contractAddress != models.USDTContractTronNile {
			t.Errorf("%s ttron tokens %+v, want Nile USDT", profile, ttron)
		}
		if len(byChain[models.ChainLTC]) != 0 || len(byChain[models.ChainTLTC]) != 0 {
			t.Errorf("%s: Litecoin has no tokens, got %+v", profile, byChain)
		}
	}
}

func TestTronAndLitecoinTestnetExplorers(t *testing.T) {
	t.Parallel()

	resources, err := resourcesForChains(testTronLitecoinSeeds(), models.ChainNetworkProfileTestnet)
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
		models.ChainTron: "https://explorer.tronql.com/nile", models.ChainTTron: "https://explorer.tronql.com/nile",
		models.ChainLTC: "https://litecoinspace.org/testnet", models.ChainTLTC: "https://litecoinspace.org/testnet",
	} {
		if explorers[chainID] != url || !faucets[chainID] {
			t.Errorf("%s: explorer %q faucet %t, want %s and a faucet", chainID, explorers[chainID], faucets[chainID], url)
		}
	}
}
