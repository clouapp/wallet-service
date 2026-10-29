package balances

import (
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/numeric"
)

// Balance is a wallet asset balance HTTP clients read. Field order and tags
// match the model wire, including embedded timestamps. A nil page stays nil;
// an empty page stays empty. A nil related wallet stays omitted. Callers pass
// the wallet body view, so share material stays off the wire.
//
// amount_* is the balance of the base address (source_address); unconsolidated_*
// is what the wallet's other deposit addresses hold, in unconsolidated_addresses
// addresses, until a consolidation sweeps it into the base; total_* is both.
type Balance struct {
	CreatedAt     *carbon.DateTime    `json:"created_at"`
	UpdatedAt     *carbon.DateTime    `json:"updated_at"`
	ID            uuid.UUID           `json:"id"`
	WalletID      uuid.UUID           `json:"wallet_id"`
	ChainID       string              `json:"chain_id"`
	AssetType     string              `json:"asset_type"`
	AssetSymbol   string              `json:"asset_symbol"`
	AssetName     *string             `json:"asset_name,omitempty"`
	AssetContract *string             `json:"asset_contract,omitempty"`
	AssetKey      string              `json:"asset_key"`
	Decimals      int                 `json:"decimals"`
	AmountRaw     string              `json:"amount_raw"`
	AmountDisplay string              `json:"amount_display"`
	PriceUSD      numeric.NullDecimal `json:"price_usd,omitzero"`
	ValueUSD      numeric.NullDecimal `json:"value_usd,omitzero"`
	SourceAddress *string             `json:"source_address,omitempty"`
	LastSyncedAt  time.Time           `json:"last_synced_at"`

	UnconsolidatedAmountRaw     string `json:"unconsolidated_amount_raw"`
	UnconsolidatedAmountDisplay string `json:"unconsolidated_amount_display"`
	UnconsolidatedAddresses     int    `json:"unconsolidated_addresses"`
	TotalAmountRaw              string `json:"total_amount_raw"`
	TotalAmountDisplay          string `json:"total_amount_display"`

	Wallet any `json:"wallet,omitempty"`
}

// BalanceFrom projects one balance. A nil wallet stays omitted.
func BalanceFrom[W any](row models.WalletAssetBalance, wallet *W) Balance {
	var related any
	if wallet != nil {
		related = wallet
	}
	unconsolidatedRaw, unconsolidatedDisplay := row.UnconsolidatedRaw, row.UnconsolidatedDisplay
	if unconsolidatedRaw == "" {
		unconsolidatedRaw, unconsolidatedDisplay = "0", "0"
	}
	totalRaw, totalDisplay := totalAmount(row.AmountRaw, row.AmountDisplay, unconsolidatedRaw, row.Decimals)
	return Balance{
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

		UnconsolidatedAmountRaw:     unconsolidatedRaw,
		UnconsolidatedAmountDisplay: unconsolidatedDisplay,
		UnconsolidatedAddresses:     row.UnconsolidatedAddresses,
		TotalAmountRaw:              totalRaw,
		TotalAmountDisplay:          totalDisplay,

		Wallet: related,
	}
}

// totalAmount adds what the other deposit addresses hold to the base address's
// balance. Nothing held keeps the base's own display; an amount that is not base
// units is answered as the base's balance alone.
func totalAmount(baseRaw, baseDisplay, unconsolidatedRaw string, decimals int) (string, string) {
	held, ok := amount.ParseBaseUnits(unconsolidatedRaw)
	if !ok || held.Sign() == 0 {
		return baseRaw, baseDisplay
	}
	base, ok := amount.ParseBaseUnits(baseRaw)
	if !ok {
		return baseRaw, baseDisplay
	}
	total := new(big.Int).Add(base, held)
	return total.String(), amount.FormatBaseUnits(total, decimals)
}

// BalancesFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func BalancesFrom[W any](rows []models.WalletAssetBalance, walletOf func(*models.Wallet) *W) []Balance {
	if rows == nil {
		return nil
	}
	views := make([]Balance, len(rows))
	for i := range rows {
		views[i] = BalanceFrom(rows[i], walletOf(rows[i].Wallet))
	}
	return views
}
