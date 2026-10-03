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

	// GetTransactionBlock returns the block number a tx was included in, or 0 if
	// the tx is not yet mined (still in the mempool). Used by the confirmation
	// loop to reconcile the block number for outbound txs (sweep/withdrawal/
	// gas_seed) that were inserted before being mined. Non-EVM chains that do
	// not need this reconciliation return (0, nil).
	GetTransactionBlock(ctx context.Context, txHash string) (uint64, error)

	RequiredConfirmations() uint64
	NativeAsset() string

	EstimateFee(ctx context.Context, req TransferRequest) (*FeeEstimate, error)

	// BuildSweep builds the transaction(s) to move `asset` from `req.From` to `req.To`
	// within the same wallet. Returns a slice because EVM may require a gas_seed tx
	// plus the sweep tx; SOL and BTC return a single element.
	BuildSweep(ctx context.Context, req SweepRequest) ([]UnsignedTx, error)

	// GasReadinessThreshold is the minimum native balance BaseAddress should hold
	// to be considered "gas-ready". Returns nil for chains where the concept does
	// not apply (e.g. BTC, where fees come from the spent UTXO).
	GasReadinessThreshold() *big.Int

	// DustThreshold is the minimum balance on a child address (in raw asset units)
	// for that address to be considered sweepable. Below this, sweeping costs more
	// in fees than the recovered value. Returns nil when asset is unknown or chain
	// cannot resolve a threshold (e.g. token USD threshold without price data).
	DustThreshold(asset string) *big.Int

	// EstimateGasPrice returns the current gas price suggestion for the chain,
	// expressed in the chain's native raw units (wei for EVM). Returns nil when
	// the chain has no gas-price concept (e.g. SOL, BTC) or when the estimate
	// cannot be fetched; the sweep planner treats a nil return as "estimate
	// unavailable" and surfaces plan.EstimatedGas = nil in that case.
	EstimateGasPrice(ctx context.Context) (*big.Int, error)
}

// TransferGasEstimator reports the gas limit an adapter will encode when it
// builds `req`. The sweep planner budgets with it so a plan never reserves less
// gas than execution can spend.
type TransferGasEstimator interface {
	EstimateTransferGasLimit(ctx context.Context, req TransferRequest) (uint64, error)
}

// L1DataFeeEstimator reports what a rollup charges a transfer's sender on top of
// gas limit × gas price for posting it to L1 (OP-stack L1 data fee), in native raw
// units; zero on networks without one.
type L1DataFeeEstimator interface {
	EstimateL1DataFee(ctx context.Context, req TransferRequest) (*big.Int, error)
}

// MPCSignatureFinalizer converts a raw MPC secp256k1 signature into the
// chain-specific serialized transaction accepted by BroadcastTransaction.
// EVM adapters implement this because an R/S signature alone is not a raw
// Ethereum transaction.
type MPCSignatureFinalizer interface {
	FinalizeMPCSignature(
		unsigned *UnsignedTx,
		signature []byte,
		publicKey []byte,
	) (*SignedTx, error)
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
	// GasPrice, when non-nil, is used by adapters that would otherwise fetch a
	// live gas price. Threading this through eliminates the double-fetch race
	// where BuildSweep sizes amounts with one price and BuildTransfer encodes
	// the tx with a drifted price. Currently honored by the EVM adapter only.
	GasPrice *big.Int `json:"gas_price,omitempty"`
}

// SweepRequest describes an intra-wallet sweep: move `Asset` from `From` to `To`
// within the same wallet. The native `From` balance is passed so adapters can
// decide whether to emit a preparatory gas_seed tx (EVM ERC-20). `FeePayer` is
// Solana-specific — when set, fees are paid by that address rather than `From`.
type SweepRequest struct {
	From          string   `json:"from"`
	To            string   `json:"to"`
	Asset         string   `json:"asset"`
	Amount        *big.Int `json:"amount,omitempty"`         // nil = total sweep (balance - buffer)
	NativeBalance *big.Int `json:"native_balance,omitempty"` // balance of `From` in native; adapter decides gas_seed
	FeePayer      *string  `json:"fee_payer,omitempty"`      // SOL only
	Token         *Token   `json:"token,omitempty"`          // nil = native asset sweep
}

type UnsignedTx struct {
	ChainID  string                 `json:"chain_id"`
	RawBytes []byte                 `json:"raw_bytes"`
	Metadata map[string]interface{} `json:"metadata"`
	// TransferAmount is what the tx delivers to its recipient in base units of the
	// asset it moves (native or token); nil when the builder does not report it.
	TransferAmount *big.Int `json:"transfer_amount,omitempty"`
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
	EventDepositPending          EventType = "deposit.pending"
	EventDepositConfirming       EventType = "deposit.confirming"
	EventDepositConfirmed        EventType = "deposit.confirmed"
	EventDepositFailed           EventType = "deposit.failed"
	EventWithdrawalPending       EventType = "withdrawal.pending"
	EventWithdrawalSigned        EventType = "withdrawal.signed"
	EventWithdrawalBroadcasting  EventType = "withdrawal.broadcasting"
	EventWithdrawalBroadcast     EventType = "withdrawal.broadcast"
	EventWithdrawalConfirmed     EventType = "withdrawal.confirmed"
	EventWithdrawalFailed        EventType = "withdrawal.failed"
	EventWithdrawalSweepRequired EventType = "withdrawal.sweep_required"
	EventSweepBroadcast          EventType = "sweep.broadcast"
	EventSweepConfirmed          EventType = "sweep.confirmed"
	EventWalletGasStatusChanged  EventType = "wallet.gas_status.changed"
)

// subscribableEvents are the event types a webhook config may subscribe to.
var subscribableEvents = map[EventType]bool{
	EventDepositPending:          true,
	EventDepositConfirming:       true,
	EventDepositConfirmed:        true,
	EventDepositFailed:           true,
	EventWithdrawalBroadcasting:  true,
	EventWithdrawalBroadcast:     true,
	EventWithdrawalConfirmed:     true,
	EventWithdrawalFailed:        true,
	EventWithdrawalSweepRequired: true,
	EventSweepBroadcast:          true,
	EventSweepConfirmed:          true,
	EventWalletGasStatusChanged:  true,
}

func IsSubscribableEvent(event string) bool {
	return subscribableEvents[EventType(event)]
}

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
