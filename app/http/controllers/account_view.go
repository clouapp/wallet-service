package controllers

import (
	"context"

	accountresource "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
)

// AccountView is the account body the dashboard serves, with its sweep limits
// read from settings. A failed limits read is the error: the view is never
// served without them.
func AccountView(ctx context.Context, limits *settings.Service, account models.Account) (accountresource.Account, error) {
	view := accountresource.AccountFrom(account)
	document, err := limits.AccountSweepLimitsWire(ctx, account.ID)
	if err != nil {
		return accountresource.Account{}, err
	}
	view.SweepLimits = document
	return view, nil
}
