package accounts

import (
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// platformAccountView is one row of GET /v1/platform/accounts. S3.4.1 names
// accounts.view and does not name fields, so the row is the smallest set the
// existing account profile and the lifecycle body already show: id, name, and
// status. Sweep limits, environment, wallet visibility, and linked accounts
// stay off.
type platformAccountView struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
}

func newPlatformAccountView(account models.Account) platformAccountView {
	return platformAccountView{
		ID:     account.ID,
		Name:   account.Name,
		Status: account.Status,
	}
}

func platformAccountViews(rows []models.Account) []platformAccountView {
	views := make([]platformAccountView, 0, len(rows))
	for i := range rows {
		views = append(views, newPlatformAccountView(rows[i]))
	}
	return views
}
