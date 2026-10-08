package controllers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/facades"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	walletbalances "github.com/macrowallets/waas/app/http/resources/dashboard/wallets/balances"
	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/security"
)

// walletNetwork is the network a wallet's chain record really points at (for
// example polygon-amoy) and whether it is a test network, so clients can pick the
// matching block explorer and flag testnet wallets whatever the account
// environment. Network is omitted when the chain cannot be resolved.
type walletNetwork struct {
	Network string `json:"network,omitempty" example:"polygon-amoy"`
	Testnet bool   `json:"testnet" example:"true"`
}

func newWalletNetwork(resolved models.ResolvedNetwork) walletNetwork {
	return walletNetwork{Network: resolved.Name, Testnet: resolved.Testnet}
}

// WalletListItem is a list entry: the wallet body, its network and the native
// and configured token balances of its last refresh (as GET /wallets/{id}/balances).
// Timestamps stay in the stored carbon format. A nil asset page becomes [] because
// the priced balance list is always a non-nil slice.
type WalletListItem struct {
	walletresource.Wallet
	walletNetwork
	Assets []walletbalances.Balance `json:"assets"`
}

func newWalletListItem(wallet models.Wallet, resolved models.ResolvedNetwork, assets []models.WalletAssetBalance) WalletListItem {
	priced := walletPricedFor(&wallet, resolved)
	return WalletListItem{
		Wallet:        walletresource.WalletFrom(*priced),
		walletNetwork: newWalletNetwork(resolved),
		Assets:        walletbalances.BalancesFrom(assetBalancesPricedFor(assets, resolved), walletresource.WalletPtr),
	}
}

func walletPricedFor(wallet *models.Wallet, resolved models.ResolvedNetwork) *models.Wallet {
	if wallet == nil || !resolved.Testnet {
		return wallet
	}
	unpriced := *wallet
	unpriced.BalanceUSD = numeric.NullDecimal{}
	return &unpriced
}

func assetBalancesPricedFor(assets []models.WalletAssetBalance, resolved models.ResolvedNetwork) []models.WalletAssetBalance {
	priced := make([]models.WalletAssetBalance, 0, len(assets))
	for _, asset := range assets {
		if resolved.Testnet {
			asset.PriceUSD = numeric.NullDecimal{}
			asset.ValueUSD = numeric.NullDecimal{}
		}
		priced = append(priced, asset)
	}
	return priced
}

// loadWalletListItems adds network and asset balances to a page of wallets, reading
// each chain and its tokens once.
func loadWalletListItems(ctx context.Context, balances *walletrecords.Balances, chains *chainsvc.Service, wallets []models.Wallet) ([]WalletListItem, error) {
	walletIDs := make([]uuid.UUID, 0, len(wallets))
	for _, wallet := range wallets {
		walletIDs = append(walletIDs, wallet.ID)
	}
	balanceRows, err := balances.ListByWallets(ctx, walletIDs)
	if err != nil {
		return nil, fmt.Errorf("list wallet asset balances: %w", err)
	}
	rowsByWallet := make(map[uuid.UUID][]models.WalletAssetBalance, len(wallets))
	for _, row := range balanceRows {
		rowsByWallet[row.WalletID] = append(rowsByWallet[row.WalletID], row)
	}

	resolveNetwork := cachedWalletNetworkResolver(ctx, chains)
	tokensByChain := make(map[string][]models.Token)
	items := make([]WalletListItem, 0, len(wallets))
	for _, wallet := range wallets {
		tokens, loaded := tokensByChain[wallet.Chain]
		if !loaded {
			tokens, err = chains.FindTokens(ctx, wallet.Chain)
			if err != nil {
				return nil, fmt.Errorf("list tokens of chain %s: %w", wallet.Chain, err)
			}
			tokensByChain[wallet.Chain] = tokens
		}
		assets := configuredAssetBalances(rowsByWallet[wallet.ID], tokens)
		items = append(items, newWalletListItem(wallet, resolveNetwork(wallet.Chain), assets))
	}
	return items, nil
}

// cachedWalletNetworkResolver resolves each chain once per request.
func cachedWalletNetworkResolver(ctx context.Context, chains *chainsvc.Service) func(chainID string) models.ResolvedNetwork {
	resolved := make(map[string]models.ResolvedNetwork)
	return func(chainID string) models.ResolvedNetwork {
		if network, ok := resolved[chainID]; ok {
			return network
		}
		network := resolveWalletChainNetwork(ctx, chains, chainID)
		resolved[chainID] = network
		return network
	}
}

// resolveWalletChainNetwork reads the wallet's chain record; a failed read leaves
// the network unknown instead of failing the wallet response.
func resolveWalletChainNetwork(ctx context.Context, chains *chainsvc.Service, chainID string) models.ResolvedNetwork {
	chainRecord, err := chains.FindByID(ctx, chainID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		chainRecord, err = nil, nil
	}
	if err != nil || chainRecord == nil {
		slog.Warn("load chain for wallet network", "chain", chainID, "error", err)
		return models.ResolvedNetwork{}
	}
	return chainRecord.ResolveNetwork(networkRPCURL(chainRecord))
}

// networkRPCURL decrypts the RPC URL only for adapters whose network is read from
// it (Solana clusters, Bitcoin testnet4); the URL never leaves this process.
func networkRPCURL(chainRecord *models.Chain) string {
	if !networkReadFromRPCURL(chainRecord.AdapterType) {
		return ""
	}
	cipher := facades.Crypt()
	if cipher == nil {
		slog.Warn("open chain rpc for wallet network", "chain", chainRecord.ID)
		return ""
	}
	storedURL, err := security.OpenSecret(cipher, chainRecord.RpcURL)
	if err != nil {
		slog.Warn("open chain rpc for wallet network", "chain", chainRecord.ID)
		return ""
	}
	rpcURL, err := models.DialEndpoint(storedURL)
	if err != nil {
		slog.Warn("open chain rpc for wallet network", "chain", chainRecord.ID)
		return ""
	}
	return rpcURL
}

func networkReadFromRPCURL(adapterType string) bool {
	return adapterType == models.AdapterTypeSolana || adapterType == models.AdapterTypeBitcoin
}

// LoadWalletListItems and ResolveWalletChainNetwork are the list wire helpers.
// Dashboard and external wallet handlers both call them so the JSON stays the same bytes.
func LoadWalletListItems(ctx context.Context, balances *walletrecords.Balances, chains *chainsvc.Service, wallets []models.Wallet) ([]WalletListItem, error) {
	return loadWalletListItems(ctx, balances, chains, wallets)
}

func ResolveWalletChainNetwork(ctx context.Context, chains *chainsvc.Service, chainID string) models.ResolvedNetwork {
	return resolveWalletChainNetwork(ctx, chains, chainID)
}

// PricedConfiguredBalances is the wallet balance list: configured assets only,
// with testnet prices removed. The dashboard balance handler calls it so the
// JSON matches the wallet view.
func PricedConfiguredBalances(ctx context.Context, chains *chainsvc.Service, chainID string, rows []models.WalletAssetBalance, tokens []models.Token) []models.WalletAssetBalance {
	return assetBalancesPricedFor(configuredAssetBalances(rows, tokens), resolveWalletChainNetwork(ctx, chains, chainID))
}
