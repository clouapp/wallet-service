package chaincatalog

import (
	"context"
)

// SeedChainResources inserts explorer / faucet links per chain.
func (cat *Catalog) SeedChainResources(_ context.Context) error {
	resources := []resourceSeed{
		{"eth", "explorer", "Etherscan", "https://etherscan.io"},
		{"teth", "explorer", "Sepolia Etherscan", "https://sepolia.etherscan.io"},
		{"teth", "faucet", "Sepolia Faucet", "https://sepoliafaucet.com"},
		{"btc", "explorer", "Blockstream", "https://blockstream.info"},
		{"tbtc", "explorer", "Blockstream Testnet", "https://blockstream.info/testnet"},
		{"tbtc", "faucet", "Bitcoin Testnet Faucet", "https://coinfaucet.eu/en/btc-testnet/"},
		{"tbtc", "faucet", "CoinFaucet", "https://coinfaucet.eu/en/"},
		{"polygon", "explorer", "Polygonscan", "https://polygonscan.com"},
		{"tpolygon", "explorer", "Amoy Polygonscan", "https://amoy.polygonscan.com"},
		{"tpolygon", "faucet", "Polygon Amoy Faucet", "https://faucet.polygon.technology"},
		{"sol", "explorer", "Solana Explorer", "https://explorer.solana.com"},
		{"tsol", "explorer", "Solana Explorer (Devnet)", "https://explorer.solana.com/?cluster=devnet"},
		{"tsol", "faucet", "Solana Devnet Faucet", "https://faucet.solana.com"},
	}

	profile, err := cat.profile()
	if err != nil {
		return err
	}
	added, err := cat.addedChainResources(profileOrMainnet(profile))
	if err != nil {
		return err
	}

	for _, r := range append(resources, added...) {
		if _, err := cat.createResourceUnlessPresent(r); err != nil {
			return err
		}
	}
	return nil
}
