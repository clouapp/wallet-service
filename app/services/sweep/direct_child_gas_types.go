package sweep

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

var ErrGasPreparationPending = errors.New("direct-child gas preparation needs confirmation")
var ErrGasPreparationBinding = errors.New("direct-child gas preparation binding changed")

// DirectChildGasBinding is the exact economic instruction whose sender needs
// native gas. ClaimID identifies the normal operation or a separately reviewed
// recovery attempt. SourceNonce is optional for a new withdrawal, pinned for a
// recovery. No method chooses another child, recipient or token.
type DirectChildGasBinding struct {
	OperationID           uuid.UUID `json:"operation_id"`
	ClaimID               uuid.UUID `json:"claim_id"`
	OriginalTransactionID uuid.UUID `json:"original_transaction_id,omitempty"`
	AccountID             uuid.UUID `json:"account_id"`
	WalletID              uuid.UUID `json:"wallet_id"`
	SourceAddressID       uuid.UUID `json:"source_address_id"`
	SourceAddress         string    `json:"source_address"`
	SourceDerivationIndex int       `json:"source_derivation_index"`
	SourceExternalUserID  string    `json:"source_external_user_id"`
	SourceNonce           *uint64   `json:"source_nonce,omitempty"`
	Chain                 string    `json:"chain"`
	NetworkID             int64     `json:"network_id"`
	Asset                 string    `json:"asset"`
	TokenContract         string    `json:"token_contract"`
	TokenDecimals         uint8     `json:"token_decimals"`
	AmountRaw             string    `json:"amount_raw"`
	Recipient             string    `json:"recipient"`
}

// DirectChildGasPlan is a bounded, expiring preparation quote, in native raw
// units. A supplied FunderNonce is mandatory to preserve during recovery.
type DirectChildGasPlan struct {
	BaseAddressID       uuid.UUID `json:"base_address_id"`
	BaseAddress         string    `json:"base_address"`
	FunderNonce         *uint64   `json:"funder_nonce,omitempty"`
	AmountWei           string    `json:"amount_wei"`
	MaxFeeWei           string    `json:"max_fee_wei"`
	MinFunderBalanceWei string    `json:"min_funder_balance_wei"`
	TokenFeeWei         string    `json:"token_fee_wei"`
	ExpiresAt           time.Time `json:"expires_at"`
}

type DirectChildGasResult struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	TxHash        string    `json:"tx_hash"`
	NetworkID     int64     `json:"network_id"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	Nonce         *uint64   `json:"nonce"`
	AmountWei     string    `json:"amount_wei"`
	FeeWei        string    `json:"fee_wei"`
	Confirmed     bool      `json:"confirmed"`
	Included      bool      `json:"included"`
}

// DirectChildGasPreparer is intentionally separate from Service so consumers
// can opt in without weakening existing mock or chain contracts. Observe never
// sends. Resume may send only the exact immutable bytes already committed.
type DirectChildGasPreparer interface {
	QuoteDirectChildGas(context.Context, DirectChildGasBinding) (*DirectChildGasPlan, error)
	PrepareDirectChildGas(context.Context, DirectChildGasBinding, DirectChildGasPlan, string) (*DirectChildGasResult, error)
	ObserveDirectChildGas(context.Context, DirectChildGasBinding) (*DirectChildGasResult, error)
	ResumeDirectChildGas(context.Context, DirectChildGasBinding) (*DirectChildGasResult, error)
}

// OperationPlanner pins an existing preparation's sender before a retry can
// ask the ordinary planner to choose another source.
type OperationPlanner interface {
	PlanForOperation(context.Context, uuid.UUID, uuid.UUID, string, string, Destination, uuid.UUID) (*Plan, error)
}

type directChildGasStore interface {
	WithinDirectChildGas(context.Context, uuid.UUID, uuid.UUID, func(context.Context) error) error
	FindByID(context.Context, uuid.UUID) (*models.Transaction, error)
	DirectChildGasNonceOccupied(context.Context, uuid.UUID, string, string, uint64) (bool, error)
}

func DirectChildGasStepID(operationID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("macro-wallets/direct-child-gas/v1/"+operationID.String()))
}
