package withdrawals

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/models"
)

// Public failure codes this resource carries in failure_reason. The strings
// are the closed code list. An empty reason stays omitted.
const (
	FailureInsufficientFunds    = resources.CodeInsufficientFunds
	FailureWalletNotGasReady    = resources.CodeWalletNotGasReady
	FailureUnsupportedChain     = resources.CodeUnsupportedChain
	FailureSweepLimitExceeded   = resources.CodeSweepLimitExceeded
	FailureInvalidPassphrase    = resources.CodeInvalidPassphrase
	FailurePassphraseTooShort   = resources.CodePassphraseTooShort
	FailureConcurrentWithdrawal = resources.CodeConcurrentWithdrawal
	FailureTooManyAttempts      = resources.CodeTooManyAttempts
	FailureSpendingLimit        = resources.CodeSpendingLimitExceeded
	FailureSpendingLimitInvalid = resources.CodeSpendingLimitInvalid
	FailureSpendingQuote        = resources.CodeSpendingLimitQuoteUnavailable
	FailureInternalError        = resources.CodeInternalError
)

// Withdrawal is the withdrawal row HTTP clients read. Field order and tags
// match the model wire, including embedded timestamps and the computed tx hash.
// A nil page stays nil; an empty page stays empty. A nil withdrawal stays null.
type Withdrawal struct {
	CreatedAt          *carbon.DateTime `json:"created_at"`
	UpdatedAt          *carbon.DateTime `json:"updated_at"`
	ID                 uuid.UUID        `json:"id"`
	WalletID           uuid.UUID        `json:"wallet_id"`
	TransactionID      *uuid.UUID       `json:"transaction_id,omitempty"`
	AccountID          *uuid.UUID       `json:"account_id,omitempty"`
	Status             string           `json:"status"`
	Amount             string           `json:"amount"`
	DestinationAddress string           `json:"destination_address"`
	FeeEstimate        string           `json:"fee_estimate,omitempty"`
	Note               string           `json:"note,omitempty"`
	CreatedBy          *uuid.UUID       `json:"created_by,omitempty"`
	FailureReason      *string          `json:"failure_reason,omitempty"`
	TxHash             string           `json:"tx_hash,omitempty"`
}

// WithdrawalFrom projects one withdrawal.
func WithdrawalFrom(withdrawal models.Withdrawal) Withdrawal {
	return Withdrawal{
		CreatedAt:          withdrawal.CreatedAt,
		UpdatedAt:          withdrawal.UpdatedAt,
		ID:                 withdrawal.ID,
		WalletID:           withdrawal.WalletID,
		TransactionID:      withdrawal.TransactionID,
		AccountID:          withdrawal.AccountID,
		Status:             withdrawal.Status,
		Amount:             withdrawal.Amount,
		DestinationAddress: withdrawal.DestinationAddress,
		FeeEstimate:        withdrawal.FeeEstimate,
		Note:               withdrawal.Note,
		CreatedBy:          withdrawal.CreatedBy,
		FailureReason:      withdrawal.FailureReason,
		TxHash:             withdrawal.TxHash,
	}
}

// WithdrawalsFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func WithdrawalsFrom(withdrawals []models.Withdrawal) []Withdrawal {
	if withdrawals == nil {
		return nil
	}
	views := make([]Withdrawal, len(withdrawals))
	for i := range withdrawals {
		views[i] = WithdrawalFrom(withdrawals[i])
	}
	return views
}

// WithdrawalPtr keeps a nil withdrawal as JSON null.
func WithdrawalPtr(withdrawal *models.Withdrawal) *Withdrawal {
	if withdrawal == nil {
		return nil
	}
	view := WithdrawalFrom(*withdrawal)
	return &view
}
