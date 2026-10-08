package seeds_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/shopspring/decimal"
	"github.com/spf13/cast"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/database/seeds"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestChains_Seed_DoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("chains.go")
	if err != nil {
		t.Fatalf("read chains seed: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"facades.Orm", "Orm()", ".Query()", ".Exec(", ".Raw("} {
		if strings.Contains(text, needle) {
			t.Fatalf("chains seed still queries outside the repository (%s)", needle)
		}
	}
}

func TestSeed_Chains_InsertsChainsAndRefreshesOnlyTheSealedEndpointOnRerun(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	if profile := strings.TrimSpace(facades.Config().GetString("vault.chains.network_profile")); profile != "" {
		t.Fatalf("expected an empty chain network profile, got %q", profile)
	}

	if err := seeds.SeedChains(ctx); err != nil {
		t.Fatalf("seed chains: %v", err)
	}
	assertSeededChains(t, ctx)

	chains := repositories.NewChainRepository(nil)
	eth, err := chains.FindByID(ctx, models.ChainETH)
	if err != nil {
		t.Fatalf("find eth: %v", err)
	}
	sealedBefore := eth.RpcURL

	gas := "7"
	dust := "8"
	if err := chains.UpdateThresholds(ctx, models.ChainETH, models.ChainThresholdWrite{
		GasReadinessThresholdRaw: &gas,
		DustThresholdNativeRaw:   &dust,
	}); err != nil {
		t.Fatalf("drift eth thresholds: %v", err)
	}

	if err := seeds.SeedChains(ctx); err != nil {
		t.Fatalf("reseed chains: %v", err)
	}

	eth, err = chains.FindByID(ctx, models.ChainETH)
	if err != nil {
		t.Fatalf("find eth after reseed: %v", err)
	}
	if derefString(eth.GasReadinessThresholdRaw) != "7" || derefString(eth.DustThresholdNativeRaw) != "8" {
		t.Fatalf("reseed rewrote thresholds on an existing chain: gas=%q dust=%q", derefString(eth.GasReadinessThresholdRaw), derefString(eth.DustThresholdNativeRaw))
	}
	if eth.Name != "Ethereum" || eth.Status != "active" || eth.RequiredConfirmations != 12 || eth.DisplayOrder != 1 {
		t.Fatalf("reseed rewrote eth identity: name=%q status=%q confirmations=%d order=%d", eth.Name, eth.Status, eth.RequiredConfirmations, eth.DisplayOrder)
	}
	if eth.RpcURL == sealedBefore {
		t.Fatal("reseed did not refresh the sealed endpoint")
	}
	requireSealedEndpoint(t, eth.RpcURL, seedEndpointPlaintext("ETH_RPC_URL", false))

	btc, err := chains.FindByID(ctx, models.ChainBTC)
	if err != nil {
		t.Fatalf("find btc after reseed: %v", err)
	}
	if btc.Name != "Bitcoin" || derefString(btc.GasReadinessThresholdRaw) != "" || derefString(btc.DustThresholdNativeRaw) != "10000" {
		t.Fatalf("reseed changed bitcoin: name=%q gas=%q dust=%q", btc.Name, derefString(btc.GasReadinessThresholdRaw), derefString(btc.DustThresholdNativeRaw))
	}
	requireSealedEndpoint(t, btc.RpcURL, seedEndpointPlaintext("BTC_RPC_URL", false))
}

func assertSeededChains(t *testing.T, ctx context.Context) {
	t.Helper()
	baseConfirmations := configuredConfirmations(t, models.ChainBase)
	arbitrumConfirmations := configuredConfirmations(t, models.ChainArbitrum)
	bscConfirmations := configuredConfirmations(t, models.ChainBSC)
	tronConfirmations := configuredConfirmations(t, models.ChainTron)
	litecoinConfirmations := configuredConfirmations(t, models.ChainLTC)
	xrpConfirmations := configuredConfirmations(t, models.ChainXRP)
	want := []seededChain{
		{id: models.ChainETH, name: "Ethereum", adapter: models.AdapterTypeEVM, native: "eth", decimals: 18, network: 1, hasNetwork: true, envVar: "ETH_RPC_URL", confirmations: 12, order: 1, gas: "5000000000000000", dust: "500000000000000", dustUSD: "1"},
		{id: models.ChainBTC, name: "Bitcoin", adapter: models.AdapterTypeBitcoin, native: "btc", decimals: 8, envVar: "BTC_RPC_URL", confirmations: 6, order: 3, gas: "", dust: "10000", dustUSD: "0"},
		{id: models.ChainPolygon, name: "Polygon", adapter: models.AdapterTypeEVM, native: types.NativeSymbolPOL, decimals: 18, network: 137, hasNetwork: true, envVar: "POLYGON_RPC_URL", confirmations: 128, order: 5, gas: "500000000000000000", dust: "100000000000000000", dustUSD: "0.1"},
		{id: models.ChainSOL, name: "Solana", adapter: models.AdapterTypeSolana, native: "sol", decimals: 9, envVar: "SOLANA_RPC_URL", confirmations: 1, order: 7, gas: "10000000", dust: "1000000", dustUSD: "1"},
		{id: models.ChainBase, name: "Base", adapter: models.AdapterTypeEVM, native: models.NativeETH, decimals: 18, network: models.EVMNetworkIDBaseMainnet, hasNetwork: true, envVar: "BASE_RPC_URL", envReference: true, confirmations: baseConfirmations, order: 9, gas: "200000000000000", dust: "20000000000000", dustUSD: "0.1"},
		{id: models.ChainArbitrum, name: "Arbitrum One", adapter: models.AdapterTypeEVM, native: models.NativeETH, decimals: 18, network: models.EVMNetworkIDArbitrumMainnet, hasNetwork: true, envVar: "ARBITRUM_RPC_URL", envReference: true, confirmations: arbitrumConfirmations, order: 11, gas: "200000000000000", dust: "20000000000000", dustUSD: "0.1"},
		{id: models.ChainBSC, name: "BNB Smart Chain", adapter: models.AdapterTypeEVM, native: models.NativeBNB, decimals: 18, network: models.EVMNetworkIDBSCMainnet, hasNetwork: true, envVar: "BSC_RPC_URL", envReference: true, confirmations: bscConfirmations, order: 13, gas: "500000000000000", dust: "50000000000000", dustUSD: "0.1"},
		{id: models.ChainTETH, name: "Sepolia", adapter: models.AdapterTypeEVM, native: "eth", decimals: 18, network: 11155111, hasNetwork: true, envVar: "TETH_RPC_URL", testnet: true, mainnet: models.ChainETH, confirmations: 12, order: 2, gas: "5000000000000000", dust: "500000000000000", dustUSD: "1"},
		{id: models.ChainTBTC, name: "Bitcoin Testnet", adapter: models.AdapterTypeBitcoin, native: "btc", decimals: 8, envVar: "TBTC_RPC_URL", testnet: true, mainnet: models.ChainBTC, confirmations: 6, order: 4, gas: "", dust: "10000", dustUSD: "0"},
		{id: models.ChainTPolygon, name: "Polygon Amoy", adapter: models.AdapterTypeEVM, native: types.NativeSymbolPOL, decimals: 18, network: 80002, hasNetwork: true, envVar: "TPOLYGON_RPC_URL", testnet: true, mainnet: models.ChainPolygon, confirmations: 128, order: 6, gas: "500000000000000000", dust: "100000000000000000", dustUSD: "0.1"},
		{id: models.ChainTSOL, name: "Solana Devnet", adapter: models.AdapterTypeSolana, native: "sol", decimals: 9, envVar: "TSOL_RPC_URL", testnet: true, mainnet: models.ChainSOL, confirmations: 1, order: 8, gas: "10000000", dust: "1000000", dustUSD: "1"},
		{id: models.ChainTBase, name: "Base Sepolia", adapter: models.AdapterTypeEVM, native: models.NativeETH, decimals: 18, network: models.EVMNetworkIDBaseSepolia, hasNetwork: true, envVar: "TBASE_RPC_URL", envReference: true, testnet: true, mainnet: models.ChainBase, confirmations: baseConfirmations, order: 10, gas: "200000000000000", dust: "20000000000000", dustUSD: "0.1"},
		{id: models.ChainTArbitrum, name: "Arbitrum Sepolia", adapter: models.AdapterTypeEVM, native: models.NativeETH, decimals: 18, network: models.EVMNetworkIDArbitrumSepolia, hasNetwork: true, envVar: "TARBITRUM_RPC_URL", envReference: true, testnet: true, mainnet: models.ChainArbitrum, confirmations: arbitrumConfirmations, order: 12, gas: "200000000000000", dust: "20000000000000", dustUSD: "0.1"},
		{id: models.ChainTBSC, name: "BNB Smart Chain Testnet", adapter: models.AdapterTypeEVM, native: models.NativeBNB, decimals: 18, network: models.EVMNetworkIDBSCTestnet, hasNetwork: true, envVar: "TBSC_RPC_URL", envReference: true, testnet: true, mainnet: models.ChainBSC, confirmations: bscConfirmations, order: 14, gas: "500000000000000", dust: "50000000000000", dustUSD: "0.1"},
		{id: models.ChainTron, name: "TRON", adapter: models.AdapterTypeTron, native: models.NativeTRX, decimals: 6, envVar: "TRON_RPC_URL", envReference: true, confirmations: tronConfirmations, order: 15, gas: "20000000", dust: "1000000", dustUSD: "1"},
		{id: models.ChainTTron, name: "TRON Nile", adapter: models.AdapterTypeTron, native: models.NativeTRX, decimals: 6, envVar: "TTRON_RPC_URL", envReference: true, testnet: true, mainnet: models.ChainTron, confirmations: tronConfirmations, order: 16, gas: "20000000", dust: "1000000", dustUSD: "1"},
		{id: models.ChainLTC, name: "Litecoin", adapter: models.AdapterTypeBitcoin, native: models.NativeLTC, decimals: 8, envVar: "LTC_RPC_URL", envReference: true, confirmations: litecoinConfirmations, order: 17, gas: "", dust: "10000", dustUSD: "0"},
		{id: models.ChainTLTC, name: "Litecoin Testnet", adapter: models.AdapterTypeBitcoin, native: models.NativeLTC, decimals: 8, envVar: "TLTC_RPC_URL", envReference: true, testnet: true, mainnet: models.ChainLTC, confirmations: litecoinConfirmations, order: 18, gas: "", dust: "10000", dustUSD: "0"},
		{id: models.ChainXRP, name: "XRP Ledger", adapter: models.AdapterTypeXRP, native: models.NativeXRP, decimals: 6, envVar: "XRP_RPC_URL", envReference: true, confirmations: xrpConfirmations, order: 19, gas: "", dust: "1", dustUSD: "0"},
		{id: models.ChainTXRP, name: "XRP Ledger Testnet", adapter: models.AdapterTypeXRP, native: models.NativeXRP, decimals: 6, envVar: "TXRP_RPC_URL", envReference: true, testnet: true, mainnet: models.ChainXRP, confirmations: xrpConfirmations, order: 20, gas: "", dust: "1", dustUSD: "0"},
	}

	chains := repositories.NewChainRepository(nil)
	found, err := chains.FindAll(ctx)
	if err != nil {
		t.Fatalf("list chains: %v", err)
	}
	if len(found) != len(want) {
		t.Fatalf("seeded %d chains, want %d", len(found), len(want))
	}
	byID := make(map[string]models.Chain, len(found))
	for _, chain := range found {
		byID[chain.ID] = chain
	}
	for _, row := range want {
		chain, ok := byID[row.id]
		if !ok {
			t.Fatalf("missing seeded chain %s", row.id)
			continue
		}
		if chain.Name != row.name || chain.AdapterType != row.adapter || chain.NativeSymbol != row.native || chain.NativeDecimals != row.decimals {
			t.Fatalf("chain %s identity: name=%q adapter=%q native=%q decimals=%d", row.id, chain.Name, chain.AdapterType, chain.NativeSymbol, chain.NativeDecimals)
		}
		if chain.IsTestnet != row.testnet || chain.RequiredConfirmations != row.confirmations || chain.DisplayOrder != row.order || chain.Status != "active" {
			t.Fatalf("chain %s flags: testnet=%v confirmations=%d order=%d status=%q", row.id, chain.IsTestnet, chain.RequiredConfirmations, chain.DisplayOrder, chain.Status)
		}
		if !sameOptionalInt(chain.NetworkID, row.network, row.hasNetwork) || !sameOptionalString(chain.MainnetChainID, row.mainnet) {
			t.Fatalf("chain %s network link does not match the seed", row.id)
		}
		if derefString(chain.GasReadinessThresholdRaw) != row.gas || derefString(chain.DustThresholdNativeRaw) != row.dust {
			t.Fatalf("chain %s thresholds: gas=%q dust=%q", row.id, derefString(chain.GasReadinessThresholdRaw), derefString(chain.DustThresholdNativeRaw))
		}
		requireDustUSD(t, row.id, chain.DustThresholdUSD, row.dustUSD)
		requireSealedEndpoint(t, chain.RpcURL, seedEndpointPlaintext(row.envVar, row.envReference))
	}
}

type seededChain struct {
	id            string
	name          string
	adapter       string
	native        string
	decimals      int
	network       int64
	hasNetwork    bool
	envVar        string
	envReference  bool
	testnet       bool
	mainnet       string
	confirmations int
	order         int
	gas           string
	dust          string
	dustUSD       string
}

func configuredConfirmations(t *testing.T, chainID string) int {
	t.Helper()
	n := facades.Config().GetInt("vault.chains.required_confirmations." + chainID)
	if n <= 0 {
		t.Fatalf("configured confirmations for %s are %d", chainID, n)
	}
	return n
}

func seedEndpointPlaintext(envVar string, envReference bool) string {
	if envReference {
		return models.RPCURLEnvPrefix + envVar
	}
	raw := cast.ToString(facades.Config().Env(envVar, ""))
	if raw == "" {
		return "https://placeholder.invalid"
	}
	return raw
}

func requireSealedEndpoint(t *testing.T, sealed, plaintext string) {
	t.Helper()
	if sealed == "" || sealed == plaintext || strings.HasPrefix(sealed, "http://") || strings.HasPrefix(sealed, "https://") || strings.HasPrefix(sealed, "env:") {
		t.Fatal("chain endpoint is missing or stored unsealed")
	}
	opened, err := facades.Crypt().DecryptString(sealed)
	if err != nil || opened != plaintext {
		t.Fatal("sealed endpoint does not round-trip to the seed source")
	}
}

func requireDustUSD(t *testing.T, chainID string, got numeric.NullDecimal, want string) {
	t.Helper()
	expected, err := decimal.NewFromString(want)
	if err != nil {
		t.Fatalf("dust usd expectation for %s: %v", chainID, err)
	}
	if !got.Valid || got.Decimal.IsNegative() || !got.Decimal.Equal(expected) {
		t.Fatalf("chain %s dust usd = %s valid=%v, want %s", chainID, got.Decimal.String(), got.Valid, want)
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func sameOptionalInt(got *int64, want int64, present bool) bool {
	if !present {
		return got == nil
	}
	return got != nil && *got == want
}

func sameOptionalString(got *string, want string) bool {
	if want == "" {
		return got == nil || *got == ""
	}
	return got != nil && *got == want
}
