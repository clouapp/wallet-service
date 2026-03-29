package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// SeedTokens inserts ERC-20 / SPL-style token rows per chain.
func SeedTokens(_ context.Context) error {
	const cmcBase = "https://s2.coinmarketcap.com/static/img/coins/64x64"

	type tokenSeed struct {
		chainID         string
		symbol          string
		name            string
		contractAddress string
		decimals        int
		iconURL         string
	}

	tokens := []tokenSeed{
		{"eth", "USDT", "Tether USD", "0xdAC17F958D2ee523a2206206994597C13D831ec7", 6, cmcBase + "/825.png"},
		{"eth", "USDC", "USD Coin", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", 6, cmcBase + "/3408.png"},
		{"eth", "WETH", "Wrapped Ether", "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", 18, cmcBase + "/2396.png"},
		{"eth", "WBTC", "Wrapped Bitcoin", "0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599", 8, cmcBase + "/3717.png"},
		{"eth", "DAI", "Dai Stablecoin", "0x6B175474E89094C44Da98b954EedeAC495271d0F", 18, cmcBase + "/4943.png"},
		{"eth", "LINK", "Chainlink", "0x514910771AF9Ca656af840dff83E8264EcF986CA", 18, cmcBase + "/1975.png"},
		{"eth", "UNI", "Uniswap", "0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984", 18, cmcBase + "/7083.png"},
		{"teth", "USDT", "Tether USD (Test)", "0x7169D38820dfd117C3FA1f22a697dBA58d90BA06", 6, cmcBase + "/825.png"},
		{"teth", "USDC", "USD Coin (Test)", "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238", 6, cmcBase + "/3408.png"},
		{"teth", "LINK", "Chainlink (Test)", "0x779877A7B0D9E8603169DdbD7836e478b4624789", 18, cmcBase + "/1975.png"},
		{"polygon", "USDT", "Tether USD", "0xc2132D05D31c914a87C6611C10748AEb04B58e8F", 6, cmcBase + "/825.png"},
		{"polygon", "USDC", "USD Coin", "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359", 6, cmcBase + "/3408.png"},
		{"polygon", "WETH", "Wrapped Ether", "0x7ceB23fD6bC0adD59E62ac25578270cFf1b9f619", 18, cmcBase + "/2396.png"},
		{"polygon", "WBTC", "Wrapped Bitcoin", "0x1BFD67037B42Cf73acF2047067bd4F2C47D9BfD6", 8, cmcBase + "/3717.png"},
		{"polygon", "DAI", "Dai Stablecoin", "0x8f3Cf7ad23Cd3CaDbD9735AFf958023239c6A063", 18, cmcBase + "/4943.png"},
		{"polygon", "LINK", "Chainlink", "0x53E0bca35eC356BD5ddDFebbD1Fc0fD03FaBad39", 18, cmcBase + "/1975.png"},
		{"tpolygon", "USDT", "Tether USD (Test)", "0xBDE550eCd4C18B3A3C522E1298DC6B1530710B13", 6, cmcBase + "/825.png"},
		{"tpolygon", "USDC", "USD Coin (Test)", "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582", 6, cmcBase + "/3408.png"},
		{"sol", "USDC", "USD Coin", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", 6, cmcBase + "/3408.png"},
		{"sol", "USDT", "Tether USD", "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", 6, cmcBase + "/825.png"},
		{"sol", "WSOL", "Wrapped SOL", "So11111111111111111111111111111111111111112", 9, cmcBase + "/5426.png"},
		{"sol", "BONK", "Bonk", "DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263", 5, cmcBase + "/23095.png"},
		{"sol", "JUP", "Jupiter", "JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN", 6, cmcBase + "/29210.png"},
		{"sol", "WIF", "dogwifhat", "EKpQGSJtjMFqKZ9KQanSqYXRcF8fBopzLHYxdM65zcjm", 6, cmcBase + "/28752.png"},
		{"tsol", "USDC", "USD Coin (Test)", "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU", 6, cmcBase + "/3408.png"},
	}

	for _, t := range tokens {
		var existing models.Token
		q := facades.Orm().Query().
			Where("chain_id", t.chainID).
			Where("contract_address", t.contractAddress)
		if err := q.First(&existing); err == nil && existing.ID != uuid.Nil {
			continue
		}
		iconURL := t.iconURL
		tok := models.Token{
			ID:              uuid.New(),
			ChainID:         t.chainID,
			Symbol:          t.symbol,
			Name:            t.name,
			ContractAddress: t.contractAddress,
			Decimals:        t.decimals,
			IconURL:         &iconURL,
			Status:          "active",
		}
		if err := facades.Orm().Query().Create(&tok); err != nil {
			return fmt.Errorf("create token %s %s: %w", t.chainID, t.symbol, err)
		}
		slog.Info("created token", "chain_id", t.chainID, "symbol", t.symbol)
	}
	return nil
}
