package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// WithdrawalView is the withdrawal row HTTP clients read. Field order and tags
// match the model wire, including embedded timestamps and the computed tx hash.
// A nil page stays nil; an empty page stays empty. A nil withdrawal stays null.
type WithdrawalView struct {
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

func newWithdrawalView(withdrawal models.Withdrawal) WithdrawalView {
	return WithdrawalView{
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

// WithdrawalViews copies a page. A nil slice stays nil; an empty slice stays empty.
func WithdrawalViews(withdrawals []models.Withdrawal) []WithdrawalView {
	if withdrawals == nil {
		return nil
	}
	views := make([]WithdrawalView, len(withdrawals))
	for i := range withdrawals {
		views[i] = newWithdrawalView(withdrawals[i])
	}
	return views
}

// WithdrawalViewPtr keeps a nil withdrawal as JSON null.
func WithdrawalViewPtr(withdrawal *models.Withdrawal) *WithdrawalView {
	if withdrawal == nil {
		return nil
	}
	view := newWithdrawalView(*withdrawal)
	return &view
}
