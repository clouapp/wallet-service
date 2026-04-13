package types

import (
	"context"
	"math/big"
	"time"
)

// ---------------------------------------------------------------------------
// Chain — universal adapter interface. Every blockchain implements this.
// Adding a new chain = implement this + register in the provider.
// ---------------------------------------------------------------------------

type Chain interface {
	ID() string
	Name() string

	DeriveAddress(masterKey []byte, index uint32) (string, error)
	ValidateAddress(address string) bool

	GetBalance(ctx context.Context, address string) (*Balance, error)
	GetTokenBalance(ctx context.Context, address string, token Token) (*Balance, error)

	BuildTransfer(ctx context.Context, req TransferRequest) (*UnsignedTx, error)
	SignTransaction(ctx context.Context, unsigned *UnsignedTx, privateKey []byte) (*SignedTx, error)
	BroadcastTransaction(ctx context.Context, signed *SignedTx) (string, error)

	GetLatestBlock(ctx context.Context) (uint64, error)
	ScanBlock(ctx context.Context, blockNum uint64) ([]DetectedTransfer, error)

	RequiredConfirmations() uint64
	NativeAsset() string

	EstimateFee(ctx context.Context, req TransferRequest) (*FeeEstimate, error)
}

type FeeEstimate struct {
	Fee      string `json:"fee"`
	FeeAsset string `json:"fee_asset"`
	GasPrice string `json:"gas_price,omitempty"`
	GasLimit uint64 `json:"gas_limit,omitempty"`
}

// ---------------------------------------------------------------------------
// Token
// ---------------------------------------------------------------------------

type Token struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Contract string `json:"contract"`
	Decimals uint8  `json:"decimals"`
	ChainID  string `json:"chain_id"`
}

// ---------------------------------------------------------------------------
// Transfer
// ---------------------------------------------------------------------------

type TransferRequest struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Amount   *big.Int `json:"amount"`
	Asset    string   `json:"asset"`
	Token    *Token   `json:"token"`
	Nonce    *uint64  `json:"nonce"`
	GasLimit *uint64  `json:"gas_limit"`
}

type UnsignedTx struct {
	ChainID  string                 `json:"chain_id"`
	RawBytes []byte                 `json:"raw_bytes"`
	Metadata map[string]interface{} `json:"metadata"`
}

type SignedTx struct {
	ChainID  string `json:"chain_id"`
	TxHash   string `json:"tx_hash"`
	RawBytes []byte `json:"raw_bytes"`
}

// ---------------------------------------------------------------------------
// Balance
// ---------------------------------------------------------------------------

type Balance struct {
	Address  string   `json:"address"`
	Asset    string   `json:"asset"`
	Amount   *big.Int `json:"amount"`
	Decimals uint8    `json:"decimals"`
	Human    string   `json:"human"`
}

// ---------------------------------------------------------------------------
// Deposit detection
// ---------------------------------------------------------------------------

type DetectedTransfer struct {
	TxHash      string    `json:"tx_hash"`
	BlockNumber uint64    `json:"block_number"`
	BlockHash   string    `json:"block_hash"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	Amount      *big.Int  `json:"amount"`
	Asset       string    `json:"asset"`
	Token       *Token    `json:"token"`
	LogIndex    uint      `json:"log_index"`
	Timestamp   time.Time `json:"timestamp"`
}

// ---------------------------------------------------------------------------
// Events + Statuses
// ---------------------------------------------------------------------------

type EventType string

const (
	EventDepositPending      EventType = "deposit.pending"
	EventDepositConfirming   EventType = "deposit.confirming"
	EventDepositConfirmed    EventType = "deposit.confirmed"
	EventDepositFailed       EventType = "deposit.failed"
	EventWithdrawalPending   EventType = "withdrawal.pending"
	EventWithdrawalSigned    EventType = "withdrawal.signed"
	EventWithdrawalBroadcast EventType = "withdrawal.broadcasting"
	EventWithdrawalConfirmed EventType = "withdrawal.confirmed"
	EventWithdrawalFailed    EventType = "withdrawal.failed"
)

type TxStatus string

const (
	TxStatusPending    TxStatus = "pending"
	TxStatusConfirming TxStatus = "confirming"
	TxStatusConfirmed  TxStatus = "confirmed"
	TxStatusFailed     TxStatus = "failed"
	TxStatusDropped    TxStatus = "dropped"
)

// ---------------------------------------------------------------------------
// Wallet status (maps to wallet_status enum)
// ---------------------------------------------------------------------------

type WalletStatus string

const (
	WalletStatusActive   WalletStatus = "active"
	WalletStatusPending  WalletStatus = "pending"
	WalletStatusArchived WalletStatus = "archived"
	WalletStatusFrozen   WalletStatus = "frozen"
)

// ---------------------------------------------------------------------------
// Read model status (maps to wallet_read_model_status enum)
// ---------------------------------------------------------------------------

type ReadModelStatus string

const (
	ReadModelIdle    ReadModelStatus = "idle"
	ReadModelSyncing ReadModelStatus = "syncing"
	ReadModelSynced  ReadModelStatus = "synced"
	ReadModelStale   ReadModelStatus = "stale"
	ReadModelFailed  ReadModelStatus = "failed"
)

// ---------------------------------------------------------------------------
// Sync status (maps to wallet_sync_status enum)
// ---------------------------------------------------------------------------

type SyncStatus string

const (
	SyncStatusIdle    SyncStatus = "idle"
	SyncStatusSyncing SyncStatus = "syncing"
	SyncStatusSynced  SyncStatus = "synced"
	SyncStatusStale   SyncStatus = "stale"
	SyncStatusFailed  SyncStatus = "failed"
)

// ---------------------------------------------------------------------------
// UTXO status (maps to utxo_status enum)
// ---------------------------------------------------------------------------

type UTXOStatus string

const (
	UTXOStatusUnspent  UTXOStatus = "unspent"
	UTXOStatusLocked   UTXOStatus = "locked"
	UTXOStatusSpent    UTXOStatus = "spent"
	UTXOStatusOrphaned UTXOStatus = "orphaned"
)

// ---------------------------------------------------------------------------
// Asset type (maps to asset_type enum)
// ---------------------------------------------------------------------------

type AssetType string

const (
	AssetTypeNative AssetType = "native"
	AssetTypeToken  AssetType = "token"
)

// ---------------------------------------------------------------------------
// Transaction direction (maps to transaction_direction enum)
// ---------------------------------------------------------------------------

type TxDirection string

const (
	TxDirectionInbound  TxDirection = "inbound"
	TxDirectionOutbound TxDirection = "outbound"
	TxDirectionSelf     TxDirection = "self"
	TxDirectionUnknown  TxDirection = "unknown"
)

// ---------------------------------------------------------------------------
// Transaction source (maps to transaction_source enum)
// ---------------------------------------------------------------------------

type TxSource string

const (
	TxSourceChain          TxSource = "chain"
	TxSourceDepositIngest  TxSource = "deposit_ingest"
	TxSourceWithdrawalFlow TxSource = "withdrawal_flow"
	TxSourceReconciliation TxSource = "reconciliation"
)

// ---------------------------------------------------------------------------
// SQS Message payloads
// ---------------------------------------------------------------------------

type WebhookMessage struct {
	EventID       string    `json:"event_id"`
	TransactionID string    `json:"transaction_id"`
	EventType     EventType `json:"event_type"`
	Payload       string    `json:"payload"` // JSON string
	DeliveryURL   string    `json:"delivery_url"`
	Secret        string    `json:"secret"`
	Attempt       int       `json:"attempt"`
}

type DepositScanEvent struct {
	Chain string `json:"chain"`
}
