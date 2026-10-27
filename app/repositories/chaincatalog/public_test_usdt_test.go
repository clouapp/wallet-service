package chaincatalog

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func TestPublicTestUSDTBindsThreeExistingExternalDeployments(t *testing.T) {
	t.Parallel()
	require.Len(t, publicTestUSDT, 3)
	for _, tc := range []struct {
		network, contract, documentedSymbol, name, provenance string
	}{
		{models.NetworkPolygonAmoy, "0x164F5A056166d8F2ce09FdAc6d040209a8C94d01", "USDTMock", "Public test USDT (Zama; on-chain USDTMock)", "https://github.com/zama-ai/protocol-registry/blob/main/testnet.md"},
		{models.NetworkSolanaDevnet, "H8UekPGwePSmQ3ttuYGPU1szyFfjZR4N53rymSFwpLPm", "devUSDT", "devUSDT (Orca public test token)", "https://github.com/orca-so/whirlpools-sdk-tutorial-legacy#devtokens"},
		{models.NetworkArbitrumSepolia, "0xE30928528f52CAEeB75fB07837e22d77D47e9c07", "USDT", "Public test USDT (Push)", "https://push.org/docs/chain/setup/smart-contract-address-book/"},
	} {
		t.Run(tc.network, func(t *testing.T) {
			seed, found := publicTestUSDTForNetwork(tc.network)
			require.True(t, found)
			require.Equal(t, models.SymbolUSDT, seed.symbol, "USDT is the logical custody key, not issuer provenance")
			require.Equal(t, tc.contract, seed.contractAddress)
			require.Equal(t, 6, seed.decimals)
			require.Equal(t, tc.name, seed.name)
			require.Equal(t, tc.documentedSymbol, publicTestUSDT[tc.network].documentedSymbol)
			require.Equal(t, tc.provenance, publicTestUSDT[tc.network].provenanceURL)
		})
	}
}

func TestPublicTestUSDTDoesNotExpandMainnetOtherNetworksOrLegacyAliases(t *testing.T) {
	t.Parallel()
	for _, network := range []string{models.NetworkEthereumMainnet, models.NetworkPolygonMainnet, models.NetworkSolanaMainnet, models.NetworkArbitrumMainnet, models.NetworkEthereumSepolia, models.NetworkBaseSepolia, models.NetworkBSCTestnet, "unknown"} {
		_, found := publicTestUSDTForNetwork(network)
		require.False(t, found, network)
	}
	seeds := append(primaryChainSeeds(), testAddedSeeds()...)
	for _, profile := range []string{models.ChainNetworkProfileMainnet, models.ChainNetworkProfileTestnet} {
		tokens, err := tokensForChains(seeds, profile)
		require.NoError(t, err)
		count := 0
		for _, seed := range tokens {
			for _, public := range publicTestUSDT {
				if sameContract(seed.contractAddress, public.seed.contractAddress) {
					count++
					require.Equal(t, models.ChainNetworkProfileTestnet, profile)
					require.False(t, models.IsTestChainID(seed.chainID), "legacy alias must retain its original seed list")
				}
			}
		}
		if profile == models.ChainNetworkProfileTestnet {
			require.Equal(t, 3, count)
		} else {
			require.Zero(t, count)
		}
	}
	for _, seed := range testRecordTokens {
		for _, public := range publicTestUSDT {
			require.False(t, sameContract(seed.contractAddress, public.seed.contractAddress))
		}
	}
}

func TestPublicTestUSDTPlannerPreservesSameAddressOnDifferentPhysicalNetwork(t *testing.T) {
	t.Parallel()
	custom := models.Token{ChainID: models.ChainETH, Symbol: models.SymbolUSDT, Name: "Unrelated operator token", ContractAddress: amoyZamaUSDT, Decimals: 18, Status: models.TokenStatusActive}
	changes, unknown := planNetworkTokens(models.ChainETH, models.NetworkEthereumSepolia, []models.Token{custom}, tokensOfNetwork(models.NetworkEthereumSepolia), tokensOfNetwork(models.NetworkEthereumMainnet))
	require.Contains(t, unknown, models.ChainETH+":"+models.SymbolUSDT+":"+amoyZamaUSDT)
	for _, change := range changes {
		require.False(t, sameContract(change.Contract, amoyZamaUSDT), "another network's public contract is still an operator token here")
	}
}

func TestPublicTestUSDTOverlayDoesNotMutateExistingNetworkLists(t *testing.T) {
	t.Parallel()
	for network := range publicTestUSDT {
		base := append([]tokenSeed(nil), baseTokensOfNetwork(network)...)
		first := tokensOfNetwork(network)
		require.Len(t, first, len(base)+1)
		first[0].name = "caller-local mutation"
		require.Equal(t, base, baseTokensOfNetwork(network))
		require.NotEqual(t, first[0].name, tokensOfNetwork(network)[0].name)
	}
}
