// Package withdrawrecovery resumes one refused EVM withdrawal at its original
// sender and nonce. It never calls the ordinary withdrawal planner or retry API.
package withdrawrecovery

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

var (
	ErrRefused             = errors.New("same-operation recovery refused")
	ErrOccupied            = errors.New("same-operation recovery already claimed")
	ErrNotFound            = errors.New("same-operation recovery not found")
	ErrConcurrent          = errors.New("same-operation recovery state changed")
	ErrNeedsReconciliation = errors.New("same-operation recovery needs chain reconciliation")
	ErrGasNotReady         = errors.New("same-operation recovery source lacks native gas")
)

// Binding is the immutable economic instruction read from the old withdrawal
// and transaction. A nonce is a pointer because zero is a required real nonce.
type Binding struct {
	OperationID           uuid.UUID `json:"operation_id"`
	OriginalTransactionID uuid.UUID `json:"original_transaction_id"`
	OriginalTxHash        string    `json:"original_tx_hash"`
	AccountID             uuid.UUID `json:"account_id"`
	WalletID              uuid.UUID `json:"wallet_id"`
	SourceAddressID       uuid.UUID `json:"source_address_id"`
	SourceAddress         string    `json:"source_address"`
	SourceDerivationIndex int       `json:"source_derivation_index"`
	SourceExternalUserID  string    `json:"source_external_user_id"`
	Chain                 string    `json:"chain"`
	NetworkID             int64     `json:"network_id"`
	Nonce                 *uint64   `json:"nonce"`
	Asset                 string    `json:"asset"`
	TokenContract         string    `json:"token_contract"`
	TokenDecimals         int       `json:"token_decimals"`
	AmountRaw             string    `json:"amount_raw"`
	Recipient             string    `json:"recipient"`
	RequestHash           string    `json:"request_hash"`
	ProviderFingerprint   string    `json:"provider_fingerprint"`
	RequiredConfirmations int       `json:"required_confirmations"`
	Finality              string    `json:"finality"`
}

// FeeEnvelope bounds the frozen replacement, independently of a changing quote.
type FeeEnvelope struct {
	MaxGasLimit     uint64 `json:"max_gas_limit"`
	MaxGasPriceWei  string `json:"max_gas_price_wei"`
	MaxNativeFeeWei string `json:"max_native_fee_wei"`
}

// GasSeedPlan is a separately reviewed prerequisite. The adapter must persist
// sealed bytes and claim transmission durably under Attempt.ID before sending.
// It may never choose another source, destination, nonce or amount.
type GasSeedPlan struct {
	BaseAddressID uuid.UUID `json:"base_address_id"`
	BaseAddress   string    `json:"base_address"`
	Nonce         *uint64   `json:"nonce"`
	AmountWei     string    `json:"amount_wei"`
	MaxFeeWei     string    `json:"max_fee_wei"`
}

type FeeQuote struct {
	GasLimit    uint64 `json:"gas_limit"`
	GasPriceWei string `json:"gas_price_wei"`
	L1FeeWei    string `json:"l1_fee_wei"`
}

// Observation contains actual current provider and chain facts. RPC absence is
// only eligibility for an exact-nonce replacement, never proof of final failure.
type Observation struct {
	Binding                Binding   `json:"binding"`
	ObservedAt             time.Time `json:"observed_at"`
	AccountEnvironment     string    `json:"account_environment"`
	WalletStatus           string    `json:"wallet_status"`
	WalletFrozen           bool      `json:"wallet_frozen"`
	ChainTestnet           bool      `json:"chain_testnet"`
	AdapterType            string    `json:"adapter_type"`
	RPCNetworkID           int64     `json:"rpc_network_id"`
	RPCsAgree              bool      `json:"rpcs_agree"`
	OriginalStatus         string    `json:"original_status"`
	BroadcastOutcome       string    `json:"broadcast_outcome"`
	LatestNonce            *uint64   `json:"latest_nonce"`
	PendingNonce           *uint64   `json:"pending_nonce"`
	OriginalKnown          bool      `json:"original_known"`
	CandidateKnown         bool      `json:"candidate_known"`
	ReceiptObserved        bool      `json:"receipt_observed"`
	SourceTokenBalanceRaw  string    `json:"source_token_balance_raw"`
	SourceNativeBalanceWei string    `json:"source_native_balance_wei"`
	Quote                  FeeQuote  `json:"quote"`
}

type Proposal struct {
	Binding      Binding      `json:"binding"`
	Fees         FeeEnvelope  `json:"fees"`
	GasSeed      *GasSeedPlan `json:"gas_seed,omitempty"`
	Observation  Observation  `json:"observation"`
	PreparedBy   string       `json:"prepared_by"`
	PreparedAt   time.Time    `json:"prepared_at"`
	ExpiresAt    time.Time    `json:"expires_at"`
	CodeRevision string       `json:"code_revision"`
}

type Review struct {
	ProposalHash string    `json:"proposal_hash"`
	EvidenceHash string    `json:"evidence_hash"`
	ReviewedBy   string    `json:"reviewed_by"`
	ReviewedAt   time.Time `json:"reviewed_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Approved     bool      `json:"approved"`
}

type Observer interface {
	Observe(context.Context, Binding, string) (*Observation, error)
}

// Signer only signs; it cannot transmit or select a new nonce/child/strategy.
type Signer interface {
	SignEVMRecoveryCall(context.Context, uuid.UUID, uuid.UUID, int64, chain.EVMCall, string) (*types.SignedTx, error)
}

type Sealer interface {
	Seal(string) (string, error)
	Open(string) (string, error)
}

type Sender interface {
	SendExact(context.Context, Binding, []byte) (string, error)
}

type GasResult struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	TxHash        string    `json:"tx_hash"`
	NetworkID     int64     `json:"network_id"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	Nonce         *uint64   `json:"nonce"`
	AmountWei     string    `json:"amount_wei"`
	FeeWei        string    `json:"fee_wei"`
	Confirmed     bool      `json:"confirmed"`
}

type GasPreparer interface {
	// Prepare is called at most once. It owns its own signed-before-send journal.
	Prepare(context.Context, *models.WithdrawalRecoveryAttempt, Binding, GasSeedPlan, string) (*GasResult, error)
	// Observe never sends. It recovers the result after a crash or unknown reply.
	Observe(context.Context, *models.WithdrawalRecoveryAttempt) (*GasResult, error)
}

// GasPreparationResumer is an explicit continuation of the same reviewed seed.
// It must recheck the physical journal under its writer lock: create only a
// missing exact step that protocol ordering proves was never sent, or replay
// only an existing step's sealed bytes. A failed Observe is not proof of absence.
// It must not re-quote the seed amount, advance its nonce or replace its claim.
type GasPreparationResumer interface {
	Resume(context.Context, *models.WithdrawalRecoveryAttempt, Binding, GasSeedPlan, string) (*GasResult, error)
}

// Store's writes must commit on a physical writer before returning. They reject
// an ambient outer transaction; a successful uncommitted claim is unsafe.
type Store interface {
	FindByOperation(context.Context, uuid.UUID) (*models.WithdrawalRecoveryAttempt, error)
	Claim(context.Context, *models.WithdrawalRecoveryAttempt) (bool, error)
	Transition(context.Context, uuid.UUID, string, string, models.WithdrawalRecoveryUpdate) (bool, error)
	RecordCandidate(context.Context, *models.WithdrawalRecoveryAttempt, *models.Transaction, string) (bool, error)
}

type Dependencies struct {
	Store        Store
	Observer     Observer
	Signer       Signer
	Sealer       Sealer
	Sender       Sender
	Gas          GasPreparer
	Now          func() time.Time
	CodeRevision string
}

type Service struct{ deps Dependencies }

// Result deliberately omits signed bytes, passphrases and key material.
type Result struct {
	OperationID   uuid.UUID `json:"operation_id"`
	AttemptID     uuid.UUID `json:"attempt_id"`
	State         string    `json:"state"`
	CandidateHash string    `json:"candidate_hash,omitempty"`
	Existing      bool      `json:"existing"`
}
