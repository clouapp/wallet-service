package walletview

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/numeric"
)

// items adds network and asset balances to a page of wallets, reading each chain
// and its tokens once.
func (s *Service) items(ctx context.Context, wallets []models.Wallet) ([]Item, error) {
	walletIDs := make([]uuid.UUID, 0, len(wallets))
	for _, wallet := range wallets {
		walletIDs = append(walletIDs, wallet.ID)
	}
	balanceRows, err := s.balances.ListByWallets(ctx, walletIDs)
	if err != nil {
		return nil, fmt.Errorf("list wallet asset balances: %w", err)
	}
	rowsByWallet := make(map[uuid.UUID][]models.WalletAssetBalance, len(wallets))
	for _, row := range balanceRows {
		rowsByWallet[row.WalletID] = append(rowsByWallet[row.WalletID], row)
	}

	networks := make(map[string]models.ResolvedNetwork)
	tokensByChain := make(map[string][]models.Token)
	items := make([]Item, 0, len(wallets))
	for _, wallet := range wallets {
		network, resolved := networks[wallet.Chain]
		if !resolved {
			network = s.Network(ctx, wallet.Chain)
			networks[wallet.Chain] = network
		}
		tokens, loaded := tokensByChain[wallet.Chain]
		if !loaded {
			tokens, err = s.chains.FindTokens(ctx, wallet.Chain)
			if err != nil {
				return nil, fmt.Errorf("list tokens of chain %s: %w", wallet.Chain, err)
			}
			tokensByChain[wallet.Chain] = tokens
		}
		assets := unpricedOnTestnet(configuredAssetBalances(rowsByWallet[wallet.ID], tokens), network)
		items = append(items, Item{Wallet: pricedFor(wallet, network), Network: network, Assets: assets})
	}
	return items, nil
}

// pricedFor drops the USD balance of a testnet wallet. The wallet is a copy.
func pricedFor(wallet models.Wallet, network models.ResolvedNetwork) models.Wallet {
	if network.Testnet {
		wallet.BalanceUSD = numeric.NullDecimal{}
	}
	return wallet
}

// unpricedOnTestnet drops the USD price and value of the balances of a testnet
// wallet. The rows are copies; the slice is never nil.
func unpricedOnTestnet(assets []models.WalletAssetBalance, network models.ResolvedNetwork) []models.WalletAssetBalance {
	priced := make([]models.WalletAssetBalance, 0, len(assets))
	for _, asset := range assets {
		if network.Testnet {
			asset.PriceUSD = numeric.NullDecimal{}
			asset.ValueUSD = numeric.NullDecimal{}
		}
		priced = append(priced, asset)
	}
	return priced
}

// Network is the network a wallet's chain record really points at (for example
// polygon-amoy) and whether it is a test network, so clients can pick the
// matching block explorer and flag testnet wallets whatever the account
// environment. A chain that cannot be read leaves the network unknown instead of
// failing the wallet response.
func (s *Service) Network(ctx context.Context, chainID string) models.ResolvedNetwork {
	chainRecord, err := s.chains.FindByID(ctx, chainID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		chainRecord, err = nil, nil
	}
	if err != nil || chainRecord == nil {
		slog.Warn("load chain for wallet network", "chain", chainID, "error", err)
		return models.ResolvedNetwork{}
	}
	return chainRecord.ResolveNetwork(s.rpcURL(chainRecord))
}

// rpcURL opens the RPC URL only for adapters whose network is read from it
// (Solana clusters, Bitcoin testnet4); the URL never leaves this process.
func (s *Service) rpcURL(chainRecord *models.Chain) string {
	if !networkReadFromRPCURL(chainRecord.AdapterType) {
		return ""
	}
	cipher := s.cipher()
	if cipher == nil {
		slog.Warn("open chain rpc for wallet network", "chain", chainRecord.ID)
		return ""
	}
	storedURL, err := settings.OpenStored(cipher, chainRecord.RpcURL)
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
