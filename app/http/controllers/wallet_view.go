package controllers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
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

// WalletView is a wallet plus its network. Field order matches the previous
// embedded response: wallet fields, then RFC 3339 created_at and updated_at in
// UTC, then network and testnet. Testnet wallets carry no USD value.
type WalletView struct {
	ID                  uuid.UUID                `json:"id"`
	Chain               string                   `json:"chain"`
	Label               string                   `json:"label,omitempty"`
	AddressIndex        int                      `json:"address_index"`
	DepositAddressID    *uuid.UUID               `json:"deposit_address_id,omitempty"`
	AccountID           *uuid.UUID               `json:"account_id,omitempty"`
	Status              string                   `json:"status"`
	FeeRateMin          *int                     `json:"fee_rate_min,omitempty"`
	FeeRateMax          *int                     `json:"fee_rate_max,omitempty"`
	FeeMultiplier       numeric.NullDecimal      `json:"fee_multiplier,omitzero"`
	RequiredApprovals   int                      `json:"required_approvals"`
	FrozenUntil         *time.Time               `json:"frozen_until,omitempty"`
	BalanceAsset        *string                  `json:"balance_asset,omitempty"`
	BalanceRaw          *string                  `json:"balance_raw,omitempty"`
	BalanceDisplay      *string                  `json:"balance,omitempty"`
	BalanceUSD          numeric.NullDecimal      `json:"balance_usd,omitzero"`
	BalanceLastSyncedAt *time.Time               `json:"balance_last_synced_at,omitempty"`
	ReadModelStatus     string                   `json:"read_model_status"`
	GasStatus           string                   `json:"gas_status"`
	GasLastCheckedAt    *time.Time               `json:"gas_last_checked_at,omitempty"`
	SweepPolicyVersion  int                      `json:"sweep_policy_version"`
	DepositAddress      *addressresource.Address `json:"deposit_address,omitempty"`
	zonedTimestamps
	walletNetwork
}

func newWalletView(wallet *models.Wallet, resolved models.ResolvedNetwork) WalletView {
	priced := walletPricedFor(wallet, resolved)
	return WalletView{
		ID:                  priced.ID,
		Chain:               priced.Chain,
		Label:               priced.Label,
		AddressIndex:        priced.AddressIndex,
		DepositAddressID:    priced.DepositAddressID,
		AccountID:           priced.AccountID,
		Status:              priced.Status,
		FeeRateMin:          priced.FeeRateMin,
		FeeRateMax:          priced.FeeRateMax,
		FeeMultiplier:       priced.FeeMultiplier,
		RequiredApprovals:   priced.RequiredApprovals,
		FrozenUntil:         priced.FrozenUntil,
		BalanceAsset:        priced.BalanceAsset,
		BalanceRaw:          priced.BalanceRaw,
		BalanceDisplay:      priced.BalanceDisplay,
		BalanceUSD:          priced.BalanceUSD,
		BalanceLastSyncedAt: priced.BalanceLastSyncedAt,
		ReadModelStatus:     priced.ReadModelStatus,
		GasStatus:           priced.GasStatus,
		GasLastCheckedAt:    priced.GasLastCheckedAt,
		SweepPolicyVersion:  priced.SweepPolicyVersion,
		DepositAddress:      addressresource.AddressPtr(priced.DepositAddress, walletresource.WalletPtr),
		zonedTimestamps:     newZonedTimestamps(wallet.CreatedAt, wallet.UpdatedAt),
		walletNetwork:       newWalletNetwork(resolved),
	}
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
func loadWalletListItems(ctx context.Context, wallets []models.Wallet) ([]WalletListItem, error) {
	walletIDs := make([]uuid.UUID, 0, len(wallets))
	for _, wallet := range wallets {
		walletIDs = append(walletIDs, wallet.ID)
	}
	balanceRows, err := container.MustMake[*walletrecords.Balances]().ListByWallets(ctx, walletIDs)
	if err != nil {
		return nil, fmt.Errorf("list wallet asset balances: %w", err)
	}
	rowsByWallet := make(map[uuid.UUID][]models.WalletAssetBalance, len(wallets))
	for _, row := range balanceRows {
		rowsByWallet[row.WalletID] = append(rowsByWallet[row.WalletID], row)
	}

	resolveNetwork := cachedWalletNetworkResolver(ctx)
	tokensByChain := make(map[string][]models.Token)
	items := make([]WalletListItem, 0, len(wallets))
	for _, wallet := range wallets {
		tokens, loaded := tokensByChain[wallet.Chain]
		if !loaded {
			tokens, err = container.MustMake[*chainsvc.Service]().FindTokens(ctx, wallet.Chain)
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
func cachedWalletNetworkResolver(ctx context.Context) func(chainID string) models.ResolvedNetwork {
	resolved := make(map[string]models.ResolvedNetwork)
	return func(chainID string) models.ResolvedNetwork {
		if network, ok := resolved[chainID]; ok {
			return network
		}
		network := resolveWalletChainNetwork(ctx, chainID)
		resolved[chainID] = network
		return network
	}
}

// resolveWalletChainNetwork reads the wallet's chain record; a failed read leaves
// the network unknown instead of failing the wallet response.
func resolveWalletChainNetwork(ctx context.Context, chainID string) models.ResolvedNetwork {
	chainRecord, err := container.MustMake[*chainsvc.Service]().FindByID(ctx, chainID)
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

// LoadWalletListItems, NewWalletView and ResolveWalletChainNetwork are the
// list/detail wire helpers. Dashboard and external wallet handlers both call
// them so the JSON stays the same bytes.
func LoadWalletListItems(ctx context.Context, wallets []models.Wallet) ([]WalletListItem, error) {
	return loadWalletListItems(ctx, wallets)
}

func NewWalletView(wallet *models.Wallet, resolved models.ResolvedNetwork) WalletView {
	return newWalletView(wallet, resolved)
}

func ResolveWalletChainNetwork(ctx context.Context, chainID string) models.ResolvedNetwork {
	return resolveWalletChainNetwork(ctx, chainID)
}

// PricedConfiguredBalances is the wallet balance list: configured assets only,
// with testnet prices removed. The dashboard balance handler calls it so the
// JSON matches the wallet view.
func PricedConfiguredBalances(ctx context.Context, chainID string, rows []models.WalletAssetBalance, tokens []models.Token) []models.WalletAssetBalance {
	return assetBalancesPricedFor(configuredAssetBalances(rows, tokens), resolveWalletChainNetwork(ctx, chainID))
}
