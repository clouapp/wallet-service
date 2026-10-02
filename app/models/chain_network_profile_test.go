package models

import "testing"

func TestEveryProfileDecidesEveryPrimaryChainAndResolvesToItsNetwork(t *testing.T) {
	t.Parallel()

	adapterByChain := map[string]string{
		ChainETH: AdapterTypeEVM, ChainPolygon: AdapterTypeEVM, ChainBTC: AdapterTypeBitcoin, ChainSOL: AdapterTypeSolana,
	}
	for _, profile := range []string{ChainNetworkProfileMainnet, ChainNetworkProfileTestnet} {
		for _, chainID := range PrimaryChainIDs {
			spec, decided, err := PrimaryChainNetwork(profile, chainID)
			if err != nil || !decided {
				t.Fatalf("%s/%s: decided=%t err=%v", profile, chainID, decided, err)
			}
			record := Chain{ID: chainID, AdapterType: adapterByChain[chainID], NetworkID: spec.NetworkID, IsTestnet: spec.IsTestnet}
			if got := record.Network(); got != spec.Network {
				t.Errorf("%s/%s: record resolves to %q, spec says %q", profile, chainID, got, spec.Network)
			}
			if IsTestnetNetwork(spec.Network) != spec.IsTestnet {
				t.Errorf("%s/%s: is_testnet %t disagrees with network %q", profile, chainID, spec.IsTestnet, spec.Network)
			}
		}
	}
}

func TestTestnetProfileUsesSepoliaAmoyBitcoinTestnetAndSolanaDevnet(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		ChainETH: NetworkEthereumSepolia, ChainPolygon: NetworkPolygonAmoy, ChainBTC: NetworkBitcoinTestnet, ChainSOL: NetworkSolanaDevnet,
	}
	for chainID, network := range want {
		spec, _, err := PrimaryChainNetwork(ChainNetworkProfileTestnet, chainID)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Network != network || !spec.IsTestnet {
			t.Errorf("%s: got %+v, want %s on a testnet", chainID, spec, network)
		}
	}
}

func TestTestnetProfileAcceptsBitcoinOnTestnet3OrTestnet4Only(t *testing.T) {
	t.Parallel()

	testnet, _, err := PrimaryChainNetwork(ChainNetworkProfileTestnet, ChainBTC)
	if err != nil {
		t.Fatal(err)
	}
	for network, want := range map[string]bool{
		NetworkBitcoinTestnet:  true,
		NetworkBitcoinTestnet4: true,
		NetworkBitcoinMainnet:  false,
		"bitcoin-signet":       false,
		"":                     false,
	} {
		if got := testnet.Accepts(network); got != want {
			t.Errorf("testnet btc Accepts(%q) = %t, want %t", network, got, want)
		}
	}

	mainnet, _, err := PrimaryChainNetwork(ChainNetworkProfileMainnet, ChainBTC)
	if err != nil {
		t.Fatal(err)
	}
	if mainnet.Accepts(NetworkBitcoinTestnet4) || !mainnet.Accepts(NetworkBitcoinMainnet) {
		t.Error("mainnet btc must accept mainnet only")
	}

	record := Chain{ID: ChainBTC, AdapterType: AdapterTypeBitcoin, IsTestnet: testnet.IsTestnet}
	if got := record.ResolveNetwork("https://mempool.space/testnet4/api").Name; !testnet.Accepts(got) {
		t.Errorf("a testnet btc record on a testnet4 rpc resolves to %q, which the profile refuses", got)
	}
}

func TestPrimaryChainNetworkReturnsACopyOfCompatibleNetworks(t *testing.T) {
	t.Parallel()

	spec, _, err := PrimaryChainNetwork(ChainNetworkProfileTestnet, ChainBTC)
	if err != nil || len(spec.CompatibleNetworks) == 0 {
		t.Fatalf("spec %+v err %v", spec, err)
	}
	spec.CompatibleNetworks[0] = NetworkBitcoinMainnet

	again, _, _ := PrimaryChainNetwork(ChainNetworkProfileTestnet, ChainBTC)
	if again.Accepts(NetworkBitcoinMainnet) {
		t.Fatal("mutating a returned spec changed the profile")
	}
}

func TestProfilesLeaveTestChainsAloneAndRejectUnknownNames(t *testing.T) {
	t.Parallel()

	if _, decided, err := PrimaryChainNetwork(ChainNetworkProfileTestnet, ChainTPolygon); err != nil || decided {
		t.Errorf("tpolygon: decided=%t err=%v, want undecided", decided, err)
	}
	if !IsTestChainID(ChainTBTC) || IsTestChainID(ChainBTC) {
		t.Error("IsTestChainID must hold for tbtc only")
	}
	if _, _, err := PrimaryChainNetwork("staging", ChainETH); err == nil {
		t.Error("unknown profile accepted")
	}
	if IsChainNetworkProfile("") {
		t.Error("empty profile accepted")
	}
	if env, err := EnvironmentForChainNetworkProfile(ChainNetworkProfileTestnet); err != nil || env != EnvironmentTest {
		t.Errorf("testnet profile environment = %q, %v", env, err)
	}
	if _, err := EnvironmentForChainNetworkProfile("staging"); err == nil {
		t.Error("unknown profile environment accepted")
	}
}
