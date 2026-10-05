package transactions

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/txkind"
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
	ID                  uuid.UUID                   `json:"id"`
	AddressID           *uuid.UUID                  `json:"address_id"`
	WalletID            uuid.UUID                   `json:"wallet_id"`
	ExternalUserID      string                      `json:"external_user_id"`
	Chain               string                      `json:"chain"`
	TxType              string                      `json:"tx_type"`
	TxHash              string                      `json:"tx_hash"`
	LogIndex            int                         `json:"log_index"`
	FromAddress         string                      `json:"from_address"`
	ToAddress           string                      `json:"to_address"`
	Amount              string                      `json:"amount"`
	Asset               string                      `json:"asset"`
	TokenContract       string                      `json:"token_contract"`
	Confirmations       int                         `json:"confirmations"`
	RequiredConfs       int                         `json:"required_confs"`
	Status              string                      `json:"status"`
	Fee                 string                      `json:"fee"`
	BlockNumber         int64                       `json:"block_number"`
	BlockHash           string                      `json:"block_hash"`
	ErrorMessage        string                      `json:"error_message"`
	IdempotencyKey      *string                     `json:"idempotency_key,omitempty"`
	ConfirmedAt         *time.Time                  `json:"confirmed_at"`
	Source              string                      `json:"source,omitempty"`
	ParentTransactionID *uuid.UUID                  `json:"parent_transaction_id,omitempty"`
	Origin              string                      `json:"origin,omitempty"`
	SyncedAt            *time.Time                  `json:"synced_at,omitempty"`
	Address             *controllers.AddressView    `json:"address,omitempty"`
	Wallet              *controllers.WalletBodyView `json:"wallet,omitempty"`
}

// Transaction is the account-level transaction response: the stored row plus its
// display type and direction. Timestamps keep the stored carbon format.
// amount is the stored unsigned decimal string.
type Transaction struct {
	transactionRecord
	Type           string `json:"type" enums:"deposit,withdrawal,sweep,consolidation,gas_funding,transfer,fee,unknown"`
	Direction      string `json:"direction" enums:"incoming,outgoing,internal,unknown"`
	ChainDirection string `json:"chain_direction,omitempty" enums:"inbound,outbound,self,unknown"`
}

// TransactionFrom projects one account transaction.
func TransactionFrom(tx models.Transaction) Transaction {
	kind := txkind.Classify(tx.TxType, tx.Origin, tx.Direction)
	return Transaction{
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
			Address:               controllers.AddressViewPtr(tx.Address),
			Wallet:                controllers.WalletBodyViewPtr(tx.Wallet),
		},
		Type:           kind.Type,
		Direction:      kind.Direction,
		ChainDirection: tx.Direction,
	}
}

// TransactionsFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func TransactionsFrom(transactions []models.Transaction) []Transaction {
	if transactions == nil {
		return nil
	}
	views := make([]Transaction, len(transactions))
	for i := range transactions {
		views[i] = TransactionFrom(transactions[i])
	}
	return views
}
