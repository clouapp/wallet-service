package providers

import (
	"errors"
	"testing"

	bitcoinchain "github.com/macrowallets/waas/app/adapters/chain/bitcoin"
	evmchain "github.com/macrowallets/waas/app/adapters/chain/evm"
	solanachain "github.com/macrowallets/waas/app/adapters/chain/solana"
	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

func TestRegister_ActiveChains_KeepsTheSameActiveSet(t *testing.T) {
	previous := openActiveChainEndpoint
	t.Cleanup(func() { openActiveChainEndpoint = previous })
	openActiveChainEndpoint = func(stored string) (string, error) {
		if stored == "closed" {
			return "", errors.New("open chain rpc")
		}
		return "https://rpc.test/chain", nil
	}

	ethNetwork := models.EVMNetworkIDEthereumMainnet
	gasRaw := "1000"
	dustRaw := "2000"
	rows := []models.Chain{
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
			NativeDecimals:        8,
			IsTestnet:             true,
			RequiredConfirmations: 3,
			RpcURL:                "sealed-btc",
		},
		{
			ID:                    "sol",
			Name:                  "Solana",
			AdapterType:           models.AdapterTypeSolana,
			NativeSymbol:          "sol",
			NativeDecimals:        9,
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
	if evmLive.NativeDecimals() != 18 {
		t.Fatalf("eth native decimals %d, want the chain row", evmLive.NativeDecimals())
	}

	btc, err := reg.Chain("btc")
	btcLive, isBTC := btc.(*bitcoinchain.BitcoinLive)
	if err != nil || !isBTC || !btcLive.IsTestnet() || btc.NativeAsset() != "btc" || btc.RequiredConfirmations() != 3 || btcLive.Endpoint() == "" || btcLive.NativeDecimals() != 8 {
		t.Fatal("btc adapter was not registered from the active chain row")
	}

	sol, err := reg.Chain("sol")
	solLive, isSOL := sol.(*solanachain.SolanaLive)
	if err != nil || !isSOL || sol.ID() != "sol" || sol.NativeAsset() != "sol" || sol.RequiredConfirmations() != 1 || solLive.NativeDecimals() != 9 {
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
