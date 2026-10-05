package providers

import (
	"context"
	"errors"
	"testing"

	bitcoinchain "github.com/macrowallets/waas/app/adapters/chain/bitcoin"
	evmchain "github.com/macrowallets/waas/app/adapters/chain/evm"
	solanachain "github.com/macrowallets/waas/app/adapters/chain/solana"
	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

func resetActiveChains() {
	activeChainMu.Lock()
	activeChainsReady = false
	activeChainRows = nil
	activeChainMu.Unlock()
}

type staticActiveChains struct {
	rows []models.Chain
	err  error
}

func (s staticActiveChains) FindActive(context.Context) ([]models.Chain, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

func TestReadActiveChains_StoresTheCatalogFromFindActive(t *testing.T) {
	resetActiveChains()
	t.Cleanup(resetActiveChains)

	networkID := models.EVMNetworkIDEthereumMainnet
	readActiveChains(staticActiveChains{rows: []models.Chain{{
		ID:          "eth",
		Name:        "Ethereum",
		AdapterType: models.AdapterTypeEVM,
		NetworkID:   &networkID,
		Status:      "active",
	}}})

	rows, ok := bootedActiveChains()
	if !ok || len(rows) != 1 || rows[0].ID != "eth" || rows[0].AdapterType != models.AdapterTypeEVM {
		t.Fatalf("catalog = %+v ok=%v", rows, ok)
	}
}

func TestReadActiveChains_LeavesAnEmptyCatalogWhenTheReadFails(t *testing.T) {
	resetActiveChains()
	t.Cleanup(resetActiveChains)

	readActiveChains(staticActiveChains{err: errors.New("db down")})

	rows, ok := bootedActiveChains()
	if !ok || len(rows) != 0 {
		t.Fatalf("catalog = %+v ok=%v", rows, ok)
	}
}

func TestBootedActiveChains_NotLoadedBeforeBoot(t *testing.T) {
	resetActiveChains()
	t.Cleanup(resetActiveChains)

	rows, ok := bootedActiveChains()
	if ok || rows != nil {
		t.Fatalf("catalog = %+v ok=%v", rows, ok)
	}
}

func TestRegisterActiveChains_KeepsTheSameActiveSet(t *testing.T) {
	previous := openActiveChainEndpoint
	t.Cleanup(func() { openActiveChainEndpoint = previous })
	openActiveChainEndpoint = func(stored string) (string, error) {
		if stored == "closed" {
			return "", errors.New("open chain rpc")
		}
		return "https://rpc.test/chain", nil
	}

	resetActiveChains()
	t.Cleanup(resetActiveChains)

	ethNetwork := models.EVMNetworkIDEthereumMainnet
	gasRaw := "1000"
	dustRaw := "2000"
	readActiveChains(staticActiveChains{rows: []models.Chain{
		{
			ID:                       "eth",
			Name:                     "Ethereum",
			AdapterType:              models.AdapterTypeEVM,
			NativeSymbol:             "eth",
			NativeDecimals:           18,
			NetworkID:                &ethNetwork,
			RequiredConfirmations:    12,
			RpcURL:                   "sealed-eth",
			GasReadinessThresholdRaw: &gasRaw,
			DustThresholdNativeRaw:   &dustRaw,
		},
		{
			ID:                    "btc",
			Name:                  "Bitcoin",
			AdapterType:           models.AdapterTypeBitcoin,
			NativeSymbol:          "btc",
			IsTestnet:             true,
			RequiredConfirmations: 3,
			RpcURL:                "sealed-btc",
		},
		{
			ID:                    "sol",
			Name:                  "Solana",
			AdapterType:           models.AdapterTypeSolana,
			NativeSymbol:          "sol",
			RequiredConfirmations: 1,
			RpcURL:                "sealed-sol",
		},
		{
			ID:          "closed",
			Name:        "Closed",
			AdapterType: models.AdapterTypeEVM,
			RpcURL:      "closed",
		},
		{
			ID:          "nope",
			Name:        "Nope",
			AdapterType: "other",
			RpcURL:      "sealed-other",
		},
	}})

	rows, ok := bootedActiveChains()
	if !ok || len(rows) != 5 {
		t.Fatalf("catalog len=%d ok=%v", len(rows), ok)
	}

	reg := chainpkg.NewRegistry()
	tokensByChain := map[string][]types.Token{
		"eth": {{Symbol: "usdt", Contract: "0xabc", Decimals: 6, ChainID: "eth"}},
	}
	networkByChain := registerActiveChains(reg, rows, tokensByChain)

	eth, err := reg.Chain("eth")
	if err != nil || eth.ID() != "eth" || eth.Name() != "Ethereum" || eth.NativeAsset() != "eth" || eth.RequiredConfirmations() != 12 {
		t.Fatalf("eth adapter id=%s name=%s asset=%s conf=%d err=%v", ethID(eth), ethName(eth), ethAsset(eth), ethConf(eth), err)
	}
	evmLive, isEVM := eth.(*evmchain.EVMLive)
	if !isEVM || evmLive.Endpoint() == "" || evmLive.GasReadinessThreshold() == nil || evmLive.GasReadinessThreshold().String() != "1000" {
		t.Fatal("eth adapter was not registered from the active chain row")
	}
	if evmLive.DustThreshold("eth") == nil || evmLive.DustThreshold("eth").String() != "2000" {
		t.Fatal("eth dust threshold was not taken from the active chain row")
	}

	btc, err := reg.Chain("btc")
	btcLive, isBTC := btc.(*bitcoinchain.BitcoinLive)
	if err != nil || !isBTC || !btcLive.IsTestnet() || btc.NativeAsset() != "btc" || btc.RequiredConfirmations() != 3 || btcLive.Endpoint() == "" {
		t.Fatal("btc adapter was not registered from the active chain row")
	}

	sol, err := reg.Chain("sol")
	_, isSOL := sol.(*solanachain.SolanaLive)
	if err != nil || !isSOL || sol.ID() != "sol" || sol.NativeAsset() != "sol" || sol.RequiredConfirmations() != 1 {
		t.Fatal("sol adapter was not registered from the active chain row")
	}

	if _, err := reg.Chain("closed"); err == nil {
		t.Fatal("a chain whose rpc did not open was registered")
	}
	if _, err := reg.Chain("nope"); err == nil {
		t.Fatal("an unknown adapter was registered")
	}

	if networkByChain["eth"] != models.NetworkEthereumMainnet ||
		networkByChain["btc"] != models.NetworkBitcoinTestnet ||
		networkByChain["sol"] != models.NetworkSolanaMainnet {
		t.Fatalf("networks = %#v", networkByChain)
	}
	if _, present := networkByChain["closed"]; present {
		t.Fatal("a chain whose rpc did not open was given a network name")
	}
	if name, present := networkByChain["nope"]; !present || name != "" {
		t.Fatalf("unknown adapter network = %q present=%v", name, present)
	}
}

func ethID(chain types.Chain) string {
	if chain == nil {
		return ""
	}
	return chain.ID()
}

func ethName(chain types.Chain) string {
	if chain == nil {
		return ""
	}
	return chain.Name()
}

func ethAsset(chain types.Chain) string {
	if chain == nil {
		return ""
	}
	return chain.NativeAsset()
}

func ethConf(chain types.Chain) uint64 {
	if chain == nil {
		return 0
	}
	return chain.RequiredConfirmations()
}
