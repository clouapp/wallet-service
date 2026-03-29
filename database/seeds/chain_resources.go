package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// SeedChainResources inserts explorer / faucet links per chain.
func SeedChainResources(_ context.Context) error {
	type resSeed struct {
		chainID string
		type_   string
		name    string
		url     string
	}

	resources := []resSeed{
		{"eth", "explorer", "Etherscan", "https://etherscan.io"},
		{"teth", "explorer", "Sepolia Etherscan", "https://sepolia.etherscan.io"},
		{"teth", "faucet", "Sepolia Faucet", "https://sepoliafaucet.com"},
		{"btc", "explorer", "Blockstream", "https://blockstream.info"},
		{"tbtc", "explorer", "Blockstream Testnet", "https://blockstream.info/testnet"},
		{"tbtc", "faucet", "Bitcoin Testnet Faucet", "https://coinfaucet.eu/en/btc-testnet"},
		{"polygon", "explorer", "Polygonscan", "https://polygonscan.com"},
		{"tpolygon", "explorer", "Amoy Polygonscan", "https://amoy.polygonscan.com"},
		{"tpolygon", "faucet", "Polygon Amoy Faucet", "https://faucet.polygon.technology"},
		{"sol", "explorer", "Solana Explorer", "https://explorer.solana.com"},
		{"tsol", "explorer", "Solana Explorer (Devnet)", "https://explorer.solana.com/?cluster=devnet"},
		{"tsol", "faucet", "Solana Devnet Faucet", "https://faucet.solana.com"},
	}

	for _, r := range resources {
		var existing models.ChainResource
		err := facades.Orm().Query().
			Where("chain_id", r.chainID).
			Where("type", r.type_).
			Where("name", r.name).
			First(&existing)
		if err == nil && existing.ID != uuid.Nil {
			continue
		}
		cr := models.ChainResource{
			ID:      uuid.New(),
			ChainID: r.chainID,
			Type:    r.type_,
			Name:    r.name,
			URL:     r.url,
			Status:  "active",
		}
		if err := facades.Orm().Query().Create(&cr); err != nil {
			return fmt.Errorf("create chain resource %s %s: %w", r.chainID, r.name, err)
		}
		slog.Info("created chain resource", "chain_id", r.chainID, "name", r.name)
	}
	return nil
}
