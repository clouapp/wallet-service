package accounts

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
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

// NewAccount shapes an account view: the row and its sweep limits.
func NewAccount(view accountsvc.View) Account {
	account := AccountFrom(view.Account)
	account.SweepLimits = view.SweepLimits
	return account
}

// AccountDetail is GET /v1/accounts/{accountId}. Existing account fields stay.
// Features is the account's active flag keys in catalog order. A missing
// account row uses the catalog default. Global rows are not included. Create
// and update do not carry this field.
type AccountDetail struct {
	Account
	Features []string `json:"features"`
}

// NewAccountDetail shapes the account detail.
func NewAccountDetail(detail accountsvc.Detail) AccountDetail {
	return AccountDetail{Account: NewAccount(detail.View), Features: detail.Features}
}

// MemberAccount is one row of GET /v1/users/me/accounts. Existing account
// fields stay; role is the caller's account_users.role, returned as stored
// (owner, admin, auditor, or user).
type MemberAccount struct {
	Account
	Role string `json:"role" example:"owner"`
}

// NewMemberAccounts shapes the caller's page of accounts. The page is never null.
func NewMemberAccounts(members []accountsvc.MemberAccount) []MemberAccount {
	items := make([]MemberAccount, 0, len(members))
	for _, member := range members {
		items = append(items, MemberAccount{Account: NewAccount(member.View), Role: member.Role})
	}
	return items
}

// DefaultAccount is PATCH /v1/users/me/default-account: the new default
// account, or null when it can no longer be read.
type DefaultAccount struct {
	Account *Account `json:"account"`
}

// NewDefaultAccount shapes the default account view. A nil view is null.
func NewDefaultAccount(view *accountsvc.View) DefaultAccount {
	if view == nil {
		return DefaultAccount{}
	}
	account := NewAccount(*view)
	return DefaultAccount{Account: &account}
}
