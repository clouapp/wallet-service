package accounts

import (
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// Account is one row of GET /v1/platform/accounts. S3.4.1 names
// accounts.view and does not name fields, so the row is the smallest set the
// existing account profile and the lifecycle body already show: id, name, and
// status. Sweep limits, environment, wallet visibility, and linked accounts
// stay off.
type Account struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
}

// AccountFrom projects one platform account row.
func AccountFrom(account models.Account) Account {
	return Account{
		ID:     account.ID,
		Name:   account.Name,
		Status: account.Status,
	}
}

// AccountsFrom projects a page. A nil slice becomes an empty list.
func AccountsFrom(rows []models.Account) []Account {
	views := make([]Account, 0, len(rows))
	for i := range rows {
		views = append(views, AccountFrom(rows[i]))
	}
	return views
}
