package wallets

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/container"
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/txkind"
)

const evmContractPrefix = "0x"

// transactionTimestamps keeps the stored carbon timestamps one level deeper
// than zonedTimestamps, so a wallet response replaces them with RFC 3339 in UTC.
type transactionTimestamps struct {
	CreatedAt *carbon.DateTime `json:"created_at"`
	UpdatedAt *carbon.DateTime `json:"updated_at"`
}

// transactionRecord is the stored transaction row the wallet transaction resource
// emits. Field order and tags match the model wire, including embedded
// timestamps. The stored direction is chain_direction on the resource; direction
// is the display sign. RawPayload stays off the wire. A nil address or wallet
// is omitted. A nested address is an address view and a nested wallet is a
// wallet body view.
type transactionRecord struct {
	transactionTimestamps
	ID                  uuid.UUID                `json:"id"`
	AddressID           *uuid.UUID               `json:"address_id"`
	WalletID            uuid.UUID                `json:"wallet_id"`
	ExternalUserID      string                   `json:"external_user_id"`
	Chain               string                   `json:"chain"`
	TxType              string                   `json:"tx_type"`
	TxHash              string                   `json:"tx_hash"`
	LogIndex            int                      `json:"log_index"`
	FromAddress         string                   `json:"from_address"`
	ToAddress           string                   `json:"to_address"`
	Amount              string                   `json:"amount"`
	Asset               string                   `json:"asset"`
	TokenContract       string                   `json:"token_contract"`
	Confirmations       int                      `json:"confirmations"`
	RequiredConfs       int                      `json:"required_confs"`
	Status              string                   `json:"status"`
	Fee                 string                   `json:"fee"`
	BlockNumber         int64                    `json:"block_number"`
	BlockHash           string                   `json:"block_hash"`
	ErrorMessage        string                   `json:"error_message"`
	IdempotencyKey      *string                  `json:"idempotency_key,omitempty"`
	ConfirmedAt         *time.Time               `json:"confirmed_at"`
	Source              string                   `json:"source,omitempty"`
	ParentTransactionID *uuid.UUID               `json:"parent_transaction_id,omitempty"`
	Origin              string                   `json:"origin,omitempty"`
	SyncedAt            *time.Time               `json:"synced_at,omitempty"`
	Address             *addressresource.Address `json:"address,omitempty"`
	Wallet              *Wallet                  `json:"wallet,omitempty"`
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
		Address:               addressresource.AddressPtr(tx.Address, WalletPtr),
		Wallet:                WalletPtr(tx.Wallet),
	}
}

// zonedTimestamps shadows the stored timestamps: carbon.DateTime marshals as
// "2006-01-02 15:04:05" without a zone, which browsers read as local time.
type zonedTimestamps struct {
	CreatedAt *time.Time `json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedAt *time.Time `json:"updated_at" swaggertype:"string" format:"date-time"`
}

func newZonedTimestamps(createdAt, updatedAt *carbon.DateTime) zonedTimestamps {
	return zonedTimestamps{
		CreatedAt: utcTime(createdAt),
		UpdatedAt: utcTime(updatedAt),
	}
}

func utcTime(value *carbon.DateTime) *time.Time {
	if value == nil || value.IsNil() || value.IsZero() {
		return nil
	}
	utc := value.StdTime().UTC()
	return &utc
}

// Transaction is a wallet transaction plus the decimals of its asset, so
// clients can display the base-unit amount. Decimals is omitted when unknown.
// created_at and updated_at are RFC 3339 in UTC, like confirmed_at.
// amount is always the stored unsigned decimal string; type and direction tell
// clients which sign to show.
type Transaction struct {
	transactionRecord
	zonedTimestamps
	Decimals *int `json:"decimals,omitempty"`
	// FeeAsset and FeeDecimals describe fee, which is always in base units of the
	// chain's native asset (a token transfer pays its fee in TRX, ETH, ...). Omitted
	// while the fee is unknown.
	FeeAsset       string `json:"fee_asset,omitempty"`
	FeeDecimals    *int   `json:"fee_decimals,omitempty"`
	Type           string `json:"type" enums:"deposit,withdrawal,sweep,consolidation,gas_funding,transfer,fee,unknown"`
	Direction      string `json:"direction" enums:"incoming,outgoing,internal,unknown"`
	ChainDirection string `json:"chain_direction,omitempty" enums:"inbound,outbound,self,unknown"`
}

// TransactionsForChain is the wallet transaction JSON, including asset
// decimals. The dashboard transaction handlers call it so the body stays the same.
func TransactionsForChain(ctx context.Context, chainID string, transactions []models.Transaction) []Transaction {
	return transactionsFrom(transactions, loadAssetDecimalsCatalog(ctx, chainID))
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
	nativeSymbols  map[string]string
	tokenDecimals  map[string]map[string]int
}

func newAssetDecimalsCatalog(chain *models.Chain, tokens []models.Token) assetDecimalsCatalog {
	catalog := assetDecimalsCatalog{
		nativeDecimals: map[string]int{},
		nativeSymbols:  map[string]string{},
		tokenDecimals:  map[string]map[string]int{},
	}
	if chain != nil && chain.ID != "" {
		catalog.nativeDecimals[chain.ID] = chain.NativeDecimals
		catalog.nativeSymbols[chain.ID] = strings.ToUpper(chain.NativeSymbol)
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

// feeAssetFor is the native asset and decimals tx.Fee is denominated in.
func (c assetDecimalsCatalog) feeAssetFor(tx models.Transaction) (string, *int) {
	if strings.TrimSpace(tx.Fee) == "" {
		return "", nil
	}
	decimals, ok := c.nativeDecimals[tx.Chain]
	symbol := c.nativeSymbols[tx.Chain]
	if !ok || symbol == "" {
		return "", nil
	}
	return symbol, &decimals
}

// transactionsFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func transactionsFrom(transactions []models.Transaction, catalog assetDecimalsCatalog) []Transaction {
	if transactions == nil {
		return nil
	}
	views := make([]Transaction, len(transactions))
	for i := range transactions {
		tx := transactions[i]
		kind := txkind.Classify(tx.TxType, tx.Origin, tx.Direction)
		feeAsset, feeDecimals := catalog.feeAssetFor(tx)
		views[i] = Transaction{
			transactionRecord: newTransactionRecord(tx),
			zonedTimestamps:   newZonedTimestamps(tx.CreatedAt, tx.UpdatedAt),
			Decimals:          catalog.decimalsFor(tx),
			FeeAsset:          feeAsset,
			FeeDecimals:       feeDecimals,
			Type:              kind.Type,
			Direction:         kind.Direction,
			ChainDirection:    tx.Direction,
		}
	}
	return views
}
