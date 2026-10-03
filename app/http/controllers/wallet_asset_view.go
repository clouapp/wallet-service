package controllers

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/txkind"
	"github.com/macrowallets/waas/pkg/types"
)

const evmContractPrefix = "0x"

// transactionTimestamps keeps the stored carbon timestamps one level deeper
// than zonedTimestamps, so a wallet response replaces them with RFC 3339 in
// UTC and an account response keeps this order at the front.
type transactionTimestamps struct {
	CreatedAt *carbon.DateTime `json:"created_at"`
	UpdatedAt *carbon.DateTime `json:"updated_at"`
}

// transactionRecord is the stored transaction row both HTTP views emit. Field
// order and tags match the model wire, including embedded timestamps. The
// stored direction is chain_direction on the views; direction is the display
// sign. RawPayload stays off the wire. A nil address or wallet is omitted. A
// nested address is an address view and a nested wallet is a wallet body view.
type transactionRecord struct {
	transactionTimestamps
	ID                  uuid.UUID       `json:"id"`
	AddressID           *uuid.UUID      `json:"address_id"`
	WalletID            uuid.UUID       `json:"wallet_id"`
	ExternalUserID      string          `json:"external_user_id"`
	Chain               string          `json:"chain"`
	TxType              string          `json:"tx_type"`
	TxHash              string          `json:"tx_hash"`
	LogIndex            int             `json:"log_index"`
	FromAddress         string          `json:"from_address"`
	ToAddress           string          `json:"to_address"`
	Amount              string          `json:"amount"`
	Asset               string          `json:"asset"`
	TokenContract       string          `json:"token_contract"`
	Confirmations       int             `json:"confirmations"`
	RequiredConfs       int             `json:"required_confs"`
	Status              string          `json:"status"`
	Fee                 string          `json:"fee"`
	BlockNumber         int64           `json:"block_number"`
	BlockHash           string          `json:"block_hash"`
	ErrorMessage        string          `json:"error_message"`
	IdempotencyKey      *string         `json:"idempotency_key,omitempty"`
	ConfirmedAt         *time.Time      `json:"confirmed_at"`
	Source              string          `json:"source,omitempty"`
	ParentTransactionID *uuid.UUID      `json:"parent_transaction_id,omitempty"`
	Origin              string          `json:"origin,omitempty"`
	SyncedAt            *time.Time      `json:"synced_at,omitempty"`
	Address             *AddressView    `json:"address,omitempty"`
	Wallet              *WalletBodyView `json:"wallet,omitempty"`
}

func newTransactionRecord(tx models.Transaction) transactionRecord {
	return transactionRecord{
		transactionTimestamps: transactionTimestamps{CreatedAt: tx.CreatedAt, UpdatedAt: tx.UpdatedAt},
		ID:                    tx.ID,
		AddressID:             tx.AddressID,
		WalletID:              tx.WalletID,
		ExternalUserID:        tx.ExternalUserID,
		Chain:                 tx.Chain,
		TxType:                tx.TxType,
		TxHash:                tx.TxHash,
		LogIndex:              tx.LogIndex,
		FromAddress:           tx.FromAddress,
		ToAddress:             tx.ToAddress,
		Amount:                tx.Amount,
		Asset:                 tx.Asset,
		TokenContract:         tx.TokenContract,
		Confirmations:         tx.Confirmations,
		RequiredConfs:         tx.RequiredConfs,
		Status:                tx.Status,
		Fee:                   tx.Fee,
		BlockNumber:           tx.BlockNumber,
		BlockHash:             tx.BlockHash,
		ErrorMessage:          tx.ErrorMessage,
		IdempotencyKey:        tx.IdempotencyKey,
		ConfirmedAt:           tx.ConfirmedAt,
		Source:                tx.Source,
		ParentTransactionID:   tx.ParentTransactionID,
		Origin:                tx.Origin,
		SyncedAt:              tx.SyncedAt,
		Address:               addressViewPtr(tx.Address),
		Wallet:                walletBodyViewPtr(tx.Wallet),
	}
}

// WalletTransactionView is a wallet transaction plus the decimals of its asset, so
// clients can display the base-unit amount. Decimals is omitted when unknown.
// created_at and updated_at are RFC 3339 in UTC, like confirmed_at.
// amount is always unsigned; type and direction tell clients which sign to show.
type WalletTransactionView struct {
	transactionRecord
	zonedTimestamps
	Decimals       *int   `json:"decimals,omitempty"`
	Type           string `json:"type" enums:"deposit,withdrawal,sweep,consolidation,gas_funding,transfer,fee,unknown"`
	Direction      string `json:"direction" enums:"incoming,outgoing,internal,unknown"`
	ChainDirection string `json:"chain_direction,omitempty" enums:"inbound,outbound,self,unknown"`
}

// TransactionView is the account-level transaction response: the stored row plus its
// display type and direction. Timestamps keep the stored carbon format.
type TransactionView struct {
	transactionRecord
	Type           string `json:"type" enums:"deposit,withdrawal,sweep,consolidation,gas_funding,transfer,fee,unknown"`
	Direction      string `json:"direction" enums:"incoming,outgoing,internal,unknown"`
	ChainDirection string `json:"chain_direction,omitempty" enums:"inbound,outbound,self,unknown"`
}

func classifyTransaction(tx models.Transaction) txkind.Kind {
	return txkind.Classify(tx.TxType, tx.Origin, tx.Direction)
}

func newTransactionView(tx models.Transaction) TransactionView {
	kind := classifyTransaction(tx)
	return TransactionView{
		transactionRecord: newTransactionRecord(tx),
		Type:              kind.Type,
		Direction:         kind.Direction,
		ChainDirection:    tx.Direction,
	}
}

// transactionViews copies a page. A nil slice stays nil; an empty slice stays empty.
func transactionViews(transactions []models.Transaction) []TransactionView {
	if transactions == nil {
		return nil
	}
	views := make([]TransactionView, len(transactions))
	for i := range transactions {
		views[i] = newTransactionView(transactions[i])
	}
	return views
}

// TransactionViews and NewTransactionView are the account-level transaction
// JSON. The external transaction handlers call them so the body stays the same.
func TransactionViews(transactions []models.Transaction) []TransactionView {
	return transactionViews(transactions)
}

func NewTransactionView(tx models.Transaction) TransactionView {
	return newTransactionView(tx)
}

// WalletTransactionViewsForChain is the wallet transaction JSON, including
// asset decimals. The dashboard transaction handlers call it so the body stays
// the same.
func WalletTransactionViewsForChain(ctx context.Context, chainID string, transactions []models.Transaction) []WalletTransactionView {
	return walletTransactionViews(transactions, loadAssetDecimalsCatalog(ctx, chainID))
}

// loadAssetDecimalsCatalog reads the chain and its active tokens; a failed read
// leaves those decimals unknown instead of failing the listing.
func loadAssetDecimalsCatalog(ctx context.Context, chainID string) assetDecimalsCatalog {
	chainRecord, chainErr := container.MustMake[*chainsvc.Service]().FindByID(ctx, chainID)
	if errors.Is(chainErr, models.ErrRepositoryNotFound) {
		chainRecord, chainErr = nil, nil
	}
	if chainErr != nil {
		slog.Warn("load chain for transaction decimals", "chain", chainID, "error", chainErr)
		chainRecord = nil
	}
	tokens, tokenErr := container.MustMake[*chainsvc.Service]().FindTokens(ctx, chainID)
	if tokenErr != nil {
		slog.Warn("load tokens for transaction decimals", "chain", chainID, "error", tokenErr)
		tokens = nil
	}
	return newAssetDecimalsCatalog(chainRecord, tokens)
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

// walletTransactionViews copies a page. A nil slice stays nil; an empty slice stays empty.
func walletTransactionViews(transactions []models.Transaction, catalog assetDecimalsCatalog) []WalletTransactionView {
	if transactions == nil {
		return nil
	}
	views := make([]WalletTransactionView, len(transactions))
	for i := range transactions {
		tx := transactions[i]
		kind := classifyTransaction(tx)
		views[i] = WalletTransactionView{
			transactionRecord: newTransactionRecord(tx),
			zonedTimestamps:   newZonedTimestamps(tx.CreatedAt, tx.UpdatedAt),
			Decimals:          catalog.decimalsFor(tx),
			Type:              kind.Type,
			Direction:         kind.Direction,
			ChainDirection:    tx.Direction,
		}
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
