package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Wallet struct {
	ID             uuid.UUID `json:"id" db:"id"`
	Chain          string    `json:"chain" db:"chain"`
	Label          string    `json:"label" db:"label"`
	MasterPubkey   string    `json:"-" db:"master_pubkey"`
	KeyVaultRef    string    `json:"-" db:"key_vault_ref"`
	DerivationPath string    `json:"derivation_path" db:"derivation_path"`
	AddressIndex   int       `json:"address_index" db:"address_index"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

type Address struct {
	ID              uuid.UUID      `json:"id" db:"id"`
	WalletID        uuid.UUID      `json:"wallet_id" db:"wallet_id"`
	Chain           string         `json:"chain" db:"chain"`
	Address         string         `json:"address" db:"address"`
	DerivationIndex int            `json:"derivation_index" db:"derivation_index"`
	ExternalUserID  string         `json:"external_user_id" db:"external_user_id"`
	Metadata        sql.NullString `json:"metadata" db:"metadata"`
	IsActive        bool           `json:"is_active" db:"is_active"`
	CreatedAt       time.Time      `json:"created_at" db:"created_at"`
}

type Transaction struct {
	ID             uuid.UUID      `json:"id" db:"id"`
	AddressID      *uuid.UUID     `json:"address_id" db:"address_id"`
	WalletID       uuid.UUID      `json:"wallet_id" db:"wallet_id"`
	ExternalUserID string         `json:"external_user_id" db:"external_user_id"`
	Chain          string         `json:"chain" db:"chain"`
	TxType         string         `json:"tx_type" db:"tx_type"`
	TxHash         sql.NullString `json:"tx_hash" db:"tx_hash"`
	FromAddress    sql.NullString `json:"from_address" db:"from_address"`
	ToAddress      string         `json:"to_address" db:"to_address"`
	Amount         string         `json:"amount" db:"amount"`
	Asset          string         `json:"asset" db:"asset"`
	TokenContract  sql.NullString `json:"token_contract" db:"token_contract"`
	Confirmations  int            `json:"confirmations" db:"confirmations"`
	RequiredConfs  int            `json:"required_confs" db:"required_confs"`
	Status         string         `json:"status" db:"status"`
	Fee            sql.NullString `json:"fee" db:"fee"`
	BlockNumber    sql.NullInt64  `json:"block_number" db:"block_number"`
	BlockHash      sql.NullString `json:"block_hash" db:"block_hash"`
	ErrorMessage   sql.NullString `json:"error_message" db:"error_message"`
	IdempotencyKey sql.NullString `json:"idempotency_key" db:"idempotency_key"`
	CreatedAt      time.Time      `json:"created_at" db:"created_at"`
	ConfirmedAt    *time.Time     `json:"confirmed_at" db:"confirmed_at"`
}

type WebhookConfig struct {
	ID        uuid.UUID `json:"id" db:"id"`
	URL       string    `json:"url" db:"url"`
	Secret    string    `json:"-" db:"secret"`
	Events    string    `json:"events" db:"events"` // postgres text[] as string
	IsActive  bool      `json:"is_active" db:"is_active"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
