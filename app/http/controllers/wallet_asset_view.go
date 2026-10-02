package controllers

import (
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/txkind"
	"github.com/macrowallets/waas/pkg/types"
)

const evmContractPrefix = "0x"

// WalletTransactionView is a wallet transaction plus the decimals of its asset, so
// clients can display the base-unit amount. Decimals is omitted when unknown.
// created_at and updated_at are RFC 3339 in UTC, like confirmed_at.
// amount is always unsigned; type and direction tell clients which sign to show.
type WalletTransactionView struct {
	models.Transaction
	zonedTimestamps
	Decimals       *int   `json:"decimals,omitempty"`
	Type           string `json:"type" enums:"deposit,withdrawal,sweep,consolidation,gas_funding,transfer,fee,unknown"`
	Direction      string `json:"direction" enums:"incoming,outgoing,internal,unknown"`
	ChainDirection string `json:"chain_direction,omitempty" enums:"inbound,outbound,self,unknown"`
}

// TransactionView is the account-level transaction response: the stored row plus its
// display type and direction. Timestamps keep the stored format of models.Transaction.
type TransactionView struct {
	models.Transaction
	Type           string `json:"type" enums:"deposit,withdrawal,sweep,consolidation,gas_funding,transfer,fee,unknown"`
	Direction      string `json:"direction" enums:"incoming,outgoing,internal,unknown"`
	ChainDirection string `json:"chain_direction,omitempty" enums:"inbound,outbound,self,unknown"`
}

func classifyTransaction(tx models.Transaction) txkind.Kind {
	return txkind.Classify(tx.TxType, tx.Origin, tx.Direction)
}

func newTransactionView(tx models.Transaction) TransactionView {
	kind := classifyTransaction(tx)
	return TransactionView{Transaction: tx, Type: kind.Type, Direction: kind.Direction, ChainDirection: tx.Direction}
}

func transactionViews(transactions []models.Transaction) []TransactionView {
	views := make([]TransactionView, 0, len(transactions))
	for _, tx := range transactions {
		views = append(views, newTransactionView(tx))
	}
	return views
}

// assetDecimalsCatalog holds the native and token decimals of one chain.
type assetDecimalsCatalog struct {
	nativeDecimals map[string]int
	tokenDecimals  map[string]map[string]int
}

func newAssetDecimalsCatalog(chain *models.Chain, tokens []models.Token) assetDecimalsCatalog {
	catalog := assetDecimalsCatalog{
		nativeDecimals: map[string]int{},
		tokenDecimals:  map[string]map[string]int{},
	}
	if chain != nil && chain.ID != "" {
		catalog.nativeDecimals[chain.ID] = chain.NativeDecimals
	}
	for _, token := range tokens {
		if catalog.tokenDecimals[token.ChainID] == nil {
			catalog.tokenDecimals[token.ChainID] = map[string]int{}
		}
		catalog.tokenDecimals[token.ChainID][contractKey(token.ContractAddress)] = token.Decimals
	}
	return catalog
}

// contractKey matches EVM contracts case-insensitively and Solana mints exactly.
func contractKey(contract string) string {
	trimmed := strings.TrimSpace(contract)
	if strings.HasPrefix(strings.ToLower(trimmed), evmContractPrefix) {
		return strings.ToLower(trimmed)
	}
	return trimmed
}

func (c assetDecimalsCatalog) decimalsFor(tx models.Transaction) *int {
	if strings.TrimSpace(tx.TokenContract) != "" {
		decimals, ok := c.tokenDecimals[tx.Chain][contractKey(tx.TokenContract)]
		if !ok {
			return nil
		}
		return &decimals
	}
	decimals, ok := c.nativeDecimals[tx.Chain]
	if !ok {
		return nil
	}
	return &decimals
}

func walletTransactionViews(transactions []models.Transaction, catalog assetDecimalsCatalog) []WalletTransactionView {
	views := make([]WalletTransactionView, 0, len(transactions))
	for _, tx := range transactions {
		kind := classifyTransaction(tx)
		views = append(views, WalletTransactionView{
			Transaction:     tx,
			zonedTimestamps: newZonedTimestamps(tx.Timestamps),
			Decimals:        catalog.decimalsFor(tx),
			Type:            kind.Type,
			Direction:       kind.Direction,
			ChainDirection:  tx.Direction,
		})
	}
	return views
}

// configuredAssetBalances keeps the native balance and the token balances whose
// contract the chain still configures, dropping rows left by removed tokens.
func configuredAssetBalances(rows []models.WalletAssetBalance, activeTokens []models.Token) []models.WalletAssetBalance {
	configured := make(map[string]struct{}, len(activeTokens))
	for _, token := range activeTokens {
		configured[contractKey(token.ContractAddress)] = struct{}{}
	}

	kept := make([]models.WalletAssetBalance, 0, len(rows))
	for _, row := range rows {
		if row.AssetType == string(types.AssetTypeNative) {
			kept = append(kept, row)
			continue
		}
		if row.AssetContract == nil {
			continue
		}
		if _, ok := configured[contractKey(*row.AssetContract)]; ok {
			kept = append(kept, row)
		}
	}
	return kept
}
