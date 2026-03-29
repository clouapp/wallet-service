package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// SeedChains inserts chain rows (mainnets before testnets for FK targets).
func SeedChains(_ context.Context) error {
	const cmcBase = "https://s2.coinmarketcap.com/static/img/coins/64x64"

	type chainSeed struct {
		id                    string
		name                  string
		adapterType           string
		nativeSymbol          string
		nativeDecimals        int
		networkID             *int64
		envVar                string
		isTestnet             bool
		mainnetChainID        *string
		requiredConfirmations int
		displayOrder          int
		iconURL               string
	}

	mainnets := []chainSeed{
		{models.ChainETH, "Ethereum", models.AdapterTypeEVM, "eth", 18, i64p(1), "ETH_RPC_URL", false, nil, 12, 1, cmcBase + "/1027.png"},
		{models.ChainBTC, "Bitcoin", models.AdapterTypeBitcoin, "btc", 8, nil, "BTC_RPC_URL", false, nil, 6, 3, cmcBase + "/1.png"},
		{models.ChainPolygon, "Polygon", models.AdapterTypeEVM, "matic", 18, i64p(137), "POLYGON_RPC_URL", false, nil, 128, 5, cmcBase + "/3890.png"},
		{models.ChainSOL, "Solana", models.AdapterTypeSolana, "sol", 9, nil, "SOLANA_RPC_URL", false, nil, 1, 7, cmcBase + "/5426.png"},
	}
	testnets := []chainSeed{
		{models.ChainTETH, "Sepolia", models.AdapterTypeEVM, "eth", 18, i64p(11155111), "TETH_RPC_URL", true, strp(models.ChainETH), 12, 2, cmcBase + "/1027.png"},
		{models.ChainTBTC, "Bitcoin Testnet", models.AdapterTypeBitcoin, "btc", 8, nil, "TBTC_RPC_URL", true, strp(models.ChainBTC), 6, 4, cmcBase + "/1.png"},
		{models.ChainTPolygon, "Polygon Amoy", models.AdapterTypeEVM, "matic", 18, i64p(80002), "TPOLYGON_RPC_URL", true, strp(models.ChainPolygon), 128, 6, cmcBase + "/3890.png"},
		{models.ChainTSOL, "Solana Devnet", models.AdapterTypeSolana, "sol", 9, nil, "TSOL_RPC_URL", true, strp(models.ChainSOL), 1, 8, cmcBase + "/5426.png"},
	}

	for _, c := range append(mainnets, testnets...) {
		var existing models.Chain
		if err := facades.Orm().Query().Where("id", c.id).First(&existing); err == nil && existing.ID != "" {
			slog.Info("chain already exists, skipping", "id", c.id)
			continue
		}
		encRPC, err := encryptRPCFromEnv(c.envVar)
		if err != nil {
			return fmt.Errorf("encrypt RPC for chain %s: %w", c.id, err)
		}
		iconURL := c.iconURL
		ch := models.Chain{
			ID:                    c.id,
			Name:                  c.name,
			AdapterType:           c.adapterType,
			NativeSymbol:          c.nativeSymbol,
			NativeDecimals:        c.nativeDecimals,
			NetworkID:             c.networkID,
			RpcURL:                encRPC,
			IsTestnet:             c.isTestnet,
			MainnetChainID:        c.mainnetChainID,
			RequiredConfirmations: c.requiredConfirmations,
			IconURL:               &iconURL,
			DisplayOrder:          c.displayOrder,
			Status:                "active",
		}
		if err := facades.Orm().Query().Create(&ch); err != nil {
			return fmt.Errorf("create chain %s: %w", c.id, err)
		}
		slog.Info("created chain", "id", c.id)
	}
	return nil
}
