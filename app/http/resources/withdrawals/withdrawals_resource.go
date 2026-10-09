package withdrawals

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
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

// List documents the paginated list of withdrawals, as pagination.Response
// writes it.
type List struct {
	Data   []Withdrawal `json:"data"`
	Total  int64        `json:"total" example:"1"`
	Limit  int          `json:"limit" example:"50"`
	Offset int          `json:"offset" example:"0"`
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

// Lookup is the external view of a withdrawal's outcome, by idempotency key.
// TxHash is set only once a transaction exists on chain; FailureReason only
// for failed withdrawals.
type Lookup struct {
	ID                 string           `json:"id" example:"80571fff-8d0b-5c1e-9a3f-2b6f0f7c1a11"`
	IdempotencyKey     string           `json:"idempotency_key" example:"80571fff-8d0b-5c1e-9a3f-2b6f0f7c1a11"`
	WalletID           string           `json:"wallet_id"`
	Status             string           `json:"status" example:"broadcast"`
	Amount             string           `json:"amount" example:"4"`
	DestinationAddress string           `json:"destination_address"`
	TxHash             *string          `json:"tx_hash"`
	TransactionStatus  *string          `json:"transaction_status" example:"confirming"`
	FailureReason      *string          `json:"failure_reason" example:"insufficient_funds"`
	CreatedAt          *carbon.DateTime `json:"created_at" swaggertype:"string"`
	UpdatedAt          *carbon.DateTime `json:"updated_at" swaggertype:"string"`
}

// NewLookup projects a withdrawal and the transaction it broadcast. A nil
// transaction leaves the hash and the transaction status null.
func NewLookup(withdrawal *models.Withdrawal, tx *models.Transaction) Lookup {
	lookup := Lookup{
		ID:                 withdrawal.ID.String(),
		IdempotencyKey:     withdrawal.ID.String(),
		WalletID:           withdrawal.WalletID.String(),
		Status:             withdrawal.Status,
		Amount:             withdrawal.Amount,
		DestinationAddress: withdrawal.DestinationAddress,
		FailureReason:      withdrawal.FailureReason,
		CreatedAt:          withdrawal.CreatedAt,
		UpdatedAt:          withdrawal.UpdatedAt,
	}
	if tx != nil {
		if tx.TxHash != "" {
			lookup.TxHash = &tx.TxHash
		}
		lookup.TransactionStatus = &tx.Status
	}
	return lookup
}

// FeeEstimate is the network fee of a transfer, in the chain's native coin.
type FeeEstimate struct {
	Fee      string `json:"fee" example:"0.00021"`
	FeeAsset string `json:"fee_asset" example:"ETH"`
	GasPrice string `json:"gas_price,omitempty"`
	GasLimit uint64 `json:"gas_limit,omitempty"`
}

// NewFeeEstimate projects a fee estimate.
func NewFeeEstimate(estimate *types.FeeEstimate) FeeEstimate {
	return FeeEstimate{
		Fee:      estimate.Fee,
		FeeAsset: estimate.FeeAsset,
		GasPrice: estimate.GasPrice,
		GasLimit: estimate.GasLimit,
	}
}
