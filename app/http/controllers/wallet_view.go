package controllers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/walletrecords"
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

// WalletBodyView is the wallet JSON the model used to emit: carbon timestamps
// first, the same names and omitempty rules, and the nested deposit address as
// an address view. MPC share material and the activation code stay off the wire.
// A nil wallet stays null.
type WalletBodyView struct {
	CreatedAt           *carbon.DateTime `json:"created_at"`
	UpdatedAt           *carbon.DateTime `json:"updated_at"`
	ID                  uuid.UUID        `json:"id"`
	Chain               string           `json:"chain"`
	Label               string           `json:"label,omitempty"`
	AddressIndex        int              `json:"address_index"`
	DepositAddressID    *uuid.UUID       `json:"deposit_address_id,omitempty"`
	AccountID           *uuid.UUID       `json:"account_id,omitempty"`
	Status              string           `json:"status"`
	FeeRateMin          *int             `json:"fee_rate_min,omitempty"`
	FeeRateMax          *int             `json:"fee_rate_max,omitempty"`
	FeeMultiplier       *float64         `json:"fee_multiplier,omitempty"`
	RequiredApprovals   int              `json:"required_approvals"`
	FrozenUntil         *time.Time       `json:"frozen_until,omitempty"`
	BalanceAsset        *string          `json:"balance_asset,omitempty"`
	BalanceRaw          *string          `json:"balance_raw,omitempty"`
	BalanceDisplay      *string          `json:"balance,omitempty"`
	BalanceUSD          *float64         `json:"balance_usd,omitempty"`
	BalanceLastSyncedAt *time.Time       `json:"balance_last_synced_at,omitempty"`
	ReadModelStatus     string           `json:"read_model_status"`
	GasStatus           string           `json:"gas_status"`
	GasLastCheckedAt    *time.Time       `json:"gas_last_checked_at,omitempty"`
	SweepPolicyVersion  int              `json:"sweep_policy_version"`
	DepositAddress      *AddressView     `json:"deposit_address,omitempty"`
}

func newWalletBodyView(wallet models.Wallet) WalletBodyView {
	return WalletBodyView{
		CreatedAt:           wallet.CreatedAt,
		UpdatedAt:           wallet.UpdatedAt,
		ID:                  wallet.ID,
		Chain:               wallet.Chain,
		Label:               wallet.Label,
		AddressIndex:        wallet.AddressIndex,
		DepositAddressID:    wallet.DepositAddressID,
		AccountID:           wallet.AccountID,
		Status:              wallet.Status,
		FeeRateMin:          wallet.FeeRateMin,
		FeeRateMax:          wallet.FeeRateMax,
		FeeMultiplier:       wallet.FeeMultiplier,
		RequiredApprovals:   wallet.RequiredApprovals,
		FrozenUntil:         wallet.FrozenUntil,
		BalanceAsset:        wallet.BalanceAsset,
		BalanceRaw:          wallet.BalanceRaw,
		BalanceDisplay:      wallet.BalanceDisplay,
		BalanceUSD:          wallet.BalanceUSD,
		BalanceLastSyncedAt: wallet.BalanceLastSyncedAt,
		ReadModelStatus:     wallet.ReadModelStatus,
		GasStatus:           wallet.GasStatus,
		GasLastCheckedAt:    wallet.GasLastCheckedAt,
		SweepPolicyVersion:  wallet.SweepPolicyVersion,
		DepositAddress:      addressViewPtr(wallet.DepositAddress),
	}
}

func walletBodyViewPtr(wallet *models.Wallet) *WalletBodyView {
	if wallet == nil {
		return nil
	}
	view := newWalletBodyView(*wallet)
	return &view
}

// NewWalletBodyView is the wallet object embedded in create responses and in a
// related balance row. It does not add network fields or reformat timestamps.
func NewWalletBodyView(wallet models.Wallet) WalletBodyView {
	return newWalletBodyView(wallet)
}

// WalletBodyViewPtr keeps a nil wallet as JSON null.
func WalletBodyViewPtr(wallet *models.Wallet) *WalletBodyView {
	return walletBodyViewPtr(wallet)
}

// WalletView is a wallet plus its network. Field order matches the previous
// embedded response: wallet fields, then RFC 3339 created_at and updated_at in
// UTC, then network and testnet. Testnet wallets carry no USD value.
type WalletView struct {
	ID                  uuid.UUID    `json:"id"`
	Chain               string       `json:"chain"`
	Label               string       `json:"label,omitempty"`
	AddressIndex        int          `json:"address_index"`
	DepositAddressID    *uuid.UUID   `json:"deposit_address_id,omitempty"`
	AccountID           *uuid.UUID   `json:"account_id,omitempty"`
	Status              string       `json:"status"`
	FeeRateMin          *int         `json:"fee_rate_min,omitempty"`
	FeeRateMax          *int         `json:"fee_rate_max,omitempty"`
	FeeMultiplier       *float64     `json:"fee_multiplier,omitempty"`
	RequiredApprovals   int          `json:"required_approvals"`
	FrozenUntil         *time.Time   `json:"frozen_until,omitempty"`
	BalanceAsset        *string      `json:"balance_asset,omitempty"`
	BalanceRaw          *string      `json:"balance_raw,omitempty"`
	BalanceDisplay      *string      `json:"balance,omitempty"`
	BalanceUSD          *float64     `json:"balance_usd,omitempty"`
	BalanceLastSyncedAt *time.Time   `json:"balance_last_synced_at,omitempty"`
	ReadModelStatus     string       `json:"read_model_status"`
	GasStatus           string       `json:"gas_status"`
	GasLastCheckedAt    *time.Time   `json:"gas_last_checked_at,omitempty"`
	SweepPolicyVersion  int          `json:"sweep_policy_version"`
	DepositAddress      *AddressView `json:"deposit_address,omitempty"`
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
		DepositAddress:      addressViewPtr(priced.DepositAddress),
		zonedTimestamps:     newZonedTimestamps(wallet.CreatedAt, wallet.UpdatedAt),
		walletNetwork:       newWalletNetwork(resolved),
	}
}

// WalletListItem is a list entry: the wallet body, its network and the native
// and configured token balances of its last refresh (as GET /wallets/{id}/balances).
// Timestamps stay in the stored carbon format. A nil asset page becomes [] because
// the priced balance list is always a non-nil slice.
type WalletListItem struct {
	WalletBodyView
	walletNetwork
	Assets []WalletAssetBalanceView `json:"assets"`
}

func newWalletListItem(wallet models.Wallet, resolved models.ResolvedNetwork, assets []models.WalletAssetBalance) WalletListItem {
	priced := walletPricedFor(&wallet, resolved)
	return WalletListItem{
		WalletBodyView: newWalletBodyView(*priced),
		walletNetwork:  newWalletNetwork(resolved),
		Assets:         WalletAssetBalanceViews(assetBalancesPricedFor(assets, resolved)),
	}
}

func walletPricedFor(wallet *models.Wallet, resolved models.ResolvedNetwork) *models.Wallet {
	if wallet == nil || !resolved.Testnet {
		return wallet
	}
	unpriced := *wallet
	unpriced.BalanceUSD = nil
	return &unpriced
}

func assetBalancesPricedFor(assets []models.WalletAssetBalance, resolved models.ResolvedNetwork) []models.WalletAssetBalance {
	priced := make([]models.WalletAssetBalance, 0, len(assets))
	for _, asset := range assets {
		if resolved.Testnet {
			asset.PriceUSD = nil
			asset.ValueUSD = nil
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
