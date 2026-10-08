package seeds

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func testXRPSeeds() []chainSeed {
	return buildAddedXRPChainSeeds(func(string) int { return testConfirmations })
}

func TestXRPSeedsOnTheProfileNetworkWithEnvRPCReferences(t *testing.T) {
	t.Parallel()

	seeds := testXRPSeeds()
	if len(seeds) != 2 {
		t.Fatalf("%d xrp seeds, want 2", len(seeds))
	}
	networks := map[string]map[string]string{
		models.ChainNetworkProfileMainnet: {
			models.ChainXRP: models.NetworkXRPLMainnet, models.ChainTXRP: models.NetworkXRPLTestnet,
		},
		models.ChainNetworkProfileTestnet: {
			models.ChainXRP: models.NetworkXRPLTestnet, models.ChainTXRP: models.NetworkXRPLTestnet,
		},
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
		if seed.adapterType != models.AdapterTypeXRP || seed.nativeSymbol != models.NativeXRP || seed.nativeDecimals != 6 {
			t.Errorf("%s: adapter %s native %s/%d", seed.id, seed.adapterType, seed.nativeSymbol, seed.nativeDecimals)
		}
		wantEnv := "XRP_RPC_URL"
		if seed.id == models.ChainTXRP {
			wantEnv = "TXRP_RPC_URL"
		}
		if !seed.rpcEnvReference || seed.envVar != wantEnv || seed.networkID != nil {
			t.Errorf("%s: rpc reference %+v", seed.id, seed)
		}
		if seed.requiredConfirmations != testConfirmations {
			t.Errorf("%s: confirmations %d", seed.id, seed.requiredConfirmations)
		}
		if models.IsTestChainID(seed.id) != seed.isTestnet {
			t.Errorf("%s: is_testnet %t", seed.id, seed.isTestnet)
		}
	}
}

func TestXRPMainnetIsSeededBeforeTheTestRecordAndHasNoTokens(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, seed := range chainSeedsWith(testXRPSeeds()) {
		if seed.mainnetChainID != nil && !seen[*seed.mainnetChainID] {
			t.Fatalf("%s references %s before it is created", seed.id, *seed.mainnetChainID)
		}
		seen[seed.id] = true
	}
	for _, id := range AddedXRPChainIDs {
		if !seen[id] {
			t.Errorf("chain %s is not seeded", id)
		}
	}
	for _, profile := range []string{models.ChainNetworkProfileMainnet, models.ChainNetworkProfileTestnet} {
		tokens, err := tokensForChains(testXRPSeeds(), profile)
		if err != nil {
			t.Fatal(err)
		}
		if len(tokens) != 0 {
			t.Errorf("%s: xrp has no issued currencies, got %+v", profile, tokens)
		}
	}
}

func TestXRPTestnetExplorerAndFaucet(t *testing.T) {
	t.Parallel()

	resources, err := resourcesForChains(testXRPSeeds(), models.ChainNetworkProfileTestnet)
	if err != nil {
		t.Fatal(err)
	}
	explorers := map[string]string{}
	faucets := map[string]bool{}
	for _, resource := range resources {
		switch resource.resourceType {
		case resourceTypeExplorer:
			explorers[resource.chainID] = resource.url
		case resourceTypeFaucet:
			faucets[resource.chainID] = true
		}
	}
	for _, chainID := range []string{models.ChainXRP, models.ChainTXRP} {
		if explorers[chainID] != "https://testnet.xrpl.org" || !faucets[chainID] {
			t.Errorf("%s: explorer %q faucet %t", chainID, explorers[chainID], faucets[chainID])
		}
	}
}
