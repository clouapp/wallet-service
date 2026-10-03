package controllers

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// WalletAssetBalanceView is a wallet asset balance HTTP clients read. Field
// order and tags match the model wire, including embedded timestamps. A nil
// page stays nil; an empty page stays empty. A nil related wallet stays omitted.
// A related wallet is the wallet body view, so share material stays off the wire.
type WalletAssetBalanceView struct {
	CreatedAt     *carbon.DateTime `json:"created_at"`
	UpdatedAt     *carbon.DateTime `json:"updated_at"`
	ID            uuid.UUID        `json:"id"`
	WalletID      uuid.UUID        `json:"wallet_id"`
	ChainID       string           `json:"chain_id"`
	AssetType     string           `json:"asset_type"`
	AssetSymbol   string           `json:"asset_symbol"`
	AssetName     *string          `json:"asset_name,omitempty"`
	AssetContract *string          `json:"asset_contract,omitempty"`
	AssetKey      string           `json:"asset_key"`
	Decimals      int              `json:"decimals"`
	AmountRaw     string           `json:"amount_raw"`
	AmountDisplay string           `json:"amount_display"`
	PriceUSD      *float64         `json:"price_usd,omitempty"`
	ValueUSD      *float64         `json:"value_usd,omitempty"`
	SourceAddress *string          `json:"source_address,omitempty"`
	LastSyncedAt  time.Time        `json:"last_synced_at"`
	Wallet        *WalletBodyView  `json:"wallet,omitempty"`
}

func newWalletAssetBalanceView(row models.WalletAssetBalance) WalletAssetBalanceView {
	return WalletAssetBalanceView{
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		ID:            row.ID,
		WalletID:      row.WalletID,
		ChainID:       row.ChainID,
		AssetType:     row.AssetType,
		AssetSymbol:   row.AssetSymbol,
		AssetName:     row.AssetName,
		AssetContract: row.AssetContract,
		AssetKey:      row.AssetKey,
		Decimals:      row.Decimals,
		AmountRaw:     row.AmountRaw,
		AmountDisplay: row.AmountDisplay,
		PriceUSD:      row.PriceUSD,
		ValueUSD:      row.ValueUSD,
		SourceAddress: row.SourceAddress,
		LastSyncedAt:  row.LastSyncedAt,
		Wallet:        walletBodyViewPtr(row.Wallet),
	}
}

// WalletAssetBalanceViews copies a page. A nil slice stays nil; an empty slice stays empty.
func WalletAssetBalanceViews(rows []models.WalletAssetBalance) []WalletAssetBalanceView {
	if rows == nil {
		return nil
	}
	views := make([]WalletAssetBalanceView, len(rows))
	for i := range rows {
		views[i] = newWalletAssetBalanceView(rows[i])
	}
	return views
}
