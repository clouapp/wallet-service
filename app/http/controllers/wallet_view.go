package controllers

import (
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
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

// WalletView is a wallet plus its network. created_at and updated_at are RFC 3339
// in UTC. Testnet wallets carry no USD value: test coins have no market price.
type WalletView struct {
	*models.Wallet
	zonedTimestamps
	walletNetwork
}

func newWalletView(wallet *models.Wallet, resolved models.ResolvedNetwork) WalletView {
	return WalletView{
		Wallet:          walletPricedFor(wallet, resolved),
		zonedTimestamps: newZonedTimestamps(wallet.Timestamps),
		walletNetwork:   newWalletNetwork(resolved),
	}
}

// WalletListItem is a list entry: the wallet fields, its network and the native
// and configured token balances of its last refresh (as GET /wallets/{id}/balances).
type WalletListItem struct {
	models.Wallet
	walletNetwork
	Assets []models.WalletAssetBalance `json:"assets"`
}

func newWalletListItem(wallet models.Wallet, resolved models.ResolvedNetwork, assets []models.WalletAssetBalance) WalletListItem {
	return WalletListItem{
		Wallet:        *walletPricedFor(&wallet, resolved),
		walletNetwork: newWalletNetwork(resolved),
		Assets:        assetBalancesPricedFor(assets, resolved),
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
func loadWalletListItems(wallets []models.Wallet) ([]WalletListItem, error) {
	walletIDs := make([]uuid.UUID, 0, len(wallets))
	for _, wallet := range wallets {
		walletIDs = append(walletIDs, wallet.ID)
	}
	balanceRows, err := container.Get().WalletAssetBalanceRepo.ListByWallets(walletIDs)
	if err != nil {
		return nil, fmt.Errorf("list wallet asset balances: %w", err)
	}
	rowsByWallet := make(map[uuid.UUID][]models.WalletAssetBalance, len(wallets))
	for _, row := range balanceRows {
		rowsByWallet[row.WalletID] = append(rowsByWallet[row.WalletID], row)
	}

	resolveNetwork := cachedWalletNetworkResolver()
	tokensByChain := make(map[string][]models.Token)
	items := make([]WalletListItem, 0, len(wallets))
	for _, wallet := range wallets {
		tokens, loaded := tokensByChain[wallet.Chain]
		if !loaded {
			tokens, err = container.Get().TokenRepo.FindByChainID(wallet.Chain)
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
func cachedWalletNetworkResolver() func(chainID string) models.ResolvedNetwork {
	resolved := make(map[string]models.ResolvedNetwork)
	return func(chainID string) models.ResolvedNetwork {
		if network, ok := resolved[chainID]; ok {
			return network
		}
		network := resolveWalletChainNetwork(chainID)
		resolved[chainID] = network
		return network
	}
}

// resolveWalletChainNetwork reads the wallet's chain record; a failed read leaves
// the network unknown instead of failing the wallet response.
func resolveWalletChainNetwork(chainID string) models.ResolvedNetwork {
	chainRecord, err := container.Get().ChainRepo.FindByID(chainID)
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
	storedURL, err := facades.Crypt().DecryptString(chainRecord.RpcURL)
	if err != nil {
		slog.Warn("decrypt chain rpc for wallet network", "chain", chainRecord.ID, "error", err)
		return ""
	}
	rpcURL, err := models.ResolveRPCURL(storedURL)
	if err != nil {
		slog.Warn("resolve chain rpc for wallet network", "chain", chainRecord.ID, "error", err)
		return ""
	}
	return rpcURL
}

func networkReadFromRPCURL(adapterType string) bool {
	return adapterType == models.AdapterTypeSolana || adapterType == models.AdapterTypeBitcoin
}
