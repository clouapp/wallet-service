package accounts

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// Account is the account row the dashboard reads. Field order and tags
// match the model wire, including the embedded timestamps. A nil pointer stays
// nil. Empty omitempty pointers stay omitted; a non-nil empty string stays "".
// SweepLimits is not an accounts column. It is a JSON string: nil omits the
// field (the contract snapshot), and a caller fills it from account_sweep_limits.
type Account struct {
	CreatedAt       *carbon.DateTime `json:"created_at"`
	UpdatedAt       *carbon.DateTime `json:"updated_at"`
	ID              uuid.UUID        `json:"id"`
	Name            string           `json:"name"`
	Status          string           `json:"status"`
	ViewAllWallets  bool             `json:"view_all_wallets"`
	Environment     string           `json:"environment"`
	LinkedAccountID *uuid.UUID       `json:"linked_account_id,omitempty"`
	SweepLimits     *string          `json:"sweep_limits,omitempty"`
}

// AccountFrom projects one account.
func AccountFrom(account models.Account) Account {
	return Account{
		CreatedAt:       account.CreatedAt,
		UpdatedAt:       account.UpdatedAt,
		ID:              account.ID,
		Name:            account.Name,
		Status:          account.Status,
		ViewAllWallets:  account.ViewAllWallets,
		Environment:     account.Environment,
		LinkedAccountID: account.LinkedAccountID,
	}
}

// AccountPtr keeps a nil account as JSON null.
func AccountPtr(account *models.Account) *Account {
	if account == nil {
		return nil
	}
	view := AccountFrom(*account)
	return &view
}
