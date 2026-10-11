package transactions

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/pagination"
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/services/txkind"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/pkg/amount"
)

// transactionTimestamps keeps the stored carbon timestamps first, so an account
// transaction response keeps this order at the front.
type transactionTimestamps struct {
	CreatedAt *carbon.DateTime `json:"created_at"`
	UpdatedAt *carbon.DateTime `json:"updated_at"`
}

// transactionRecord is the stored transaction row the account response emits.
// Field order and tags match the model wire, including embedded timestamps.
// The stored direction is chain_direction; direction is the display sign.
// RawPayload stays off the wire. A nil address or wallet is omitted. A nested
// address is an address view and a nested wallet is a wallet body view.
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
	DestinationTag      *uint32                  `json:"destination_tag,omitempty"`
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
	Wallet              *walletresource.Wallet   `json:"wallet,omitempty"`
}

// Transaction is the account-level transaction response: the stored row plus its
// display type and direction. Timestamps keep the stored carbon format.
// amount is the stored unsigned integer string of base units.
//
// transaction_id, amount_base_units, decimals and required_confirmations repeat,
// under the names and in the formats of the deposit.* webhook data, what a
// receiver needs to credit a deposit, so it reads a listed deposit with its
// webhook parser. amount is the exception: it stays in base units here, while
// the webhook sends the decimal rendering (amount_format "decimal"), so a parser
// that cross-checks amount drops it first. amount_base_units and decimals are
// omitted when the amount is not an integer of base units or the asset's
// decimals are unknown.
type Transaction struct {
	transactionRecord
	Type                  string `json:"type" enums:"deposit,withdrawal,sweep,consolidation,gas_funding,transfer,fee,unknown"`
	Direction             string `json:"direction" enums:"incoming,outgoing,internal,unknown"`
	ChainDirection        string `json:"chain_direction,omitempty" enums:"inbound,outbound,self,unknown"`
	TransactionID         string `json:"transaction_id" format:"uuid"`
	AmountBaseUnits       string `json:"amount_base_units,omitempty" example:"1500000"`
	Decimals              *int   `json:"decimals,omitempty" example:"6"`
	RequiredConfirmations int    `json:"required_confirmations" example:"12"`
}

// TransactionFrom projects one account transaction and the decimals of its asset.
func TransactionFrom(row withdraw.AccountTransaction) Transaction {
	tx := row.Transaction
	kind := txkind.Classify(tx.TxType, tx.Origin, tx.Direction)
	view := Transaction{
		transactionRecord: transactionRecord{
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
			DestinationTag:        tx.DestinationTag,
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
			Address:               addressresource.AddressPtr(tx.Address, walletresource.WalletPtr),
			Wallet:                walletresource.WalletPtr(tx.Wallet),
		},
		Type:                  kind.Type,
		Direction:             kind.Direction,
		ChainDirection:        tx.Direction,
		TransactionID:         tx.ID.String(),
		RequiredConfirmations: tx.RequiredConfs,
	}
	if baseUnits, ok := amount.ParseBaseUnits(tx.Amount); ok {
		view.AmountBaseUnits = baseUnits.String()
		view.Decimals = row.Decimals
	}
	return view
}

// TransactionsFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func TransactionsFrom(rows []withdraw.AccountTransaction) []Transaction {
	if rows == nil {
		return nil
	}
	views := make([]Transaction, len(rows))
	for i := range rows {
		views[i] = TransactionFrom(rows[i])
	}
	return views
}

// NextCursor is the cursor of the page that starts after next, nil on the last page.
func NextCursor(next *withdraw.TransactionPosition) *string {
	if next == nil {
		return nil
	}
	cursor := pagination.EncodeCursor(next.UpdatedAt, next.ID)
	return &cursor
}

// List documents the paginated list of transactions, as pagination.Response
// writes it. next_cursor is there only when the request names updated_since or
// cursor (pagination.CursorResponse); it is null on the last page and offset is
// then always 0.
type List struct {
	Data       []Transaction `json:"data"`
	Total      int64         `json:"total" example:"1"`
	Limit      int           `json:"limit" example:"50"`
	Offset     int           `json:"offset" example:"0"`
	NextCursor *string       `json:"next_cursor,omitempty" example:"eyJ1cGRhdGVkX2F0IjoiMjAyNi0xMC0xMVQxMjowMDowMFoiLCJpZCI6IjdkM2M5ZjNlLTJiMWEtNGM1ZC04ZTZmLTBhMWIyYzNkNGU1ZiJ9"`
}
