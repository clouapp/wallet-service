package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletManageWebhooks refuses POST /v1/wallets/{walletId}/webhooks,
// POST /v1/wallets/{walletId}/webhooks/{webhookId}/test, and
// DELETE /v1/wallets/{walletId}/webhooks/{webhookId} unless the Gate's
// wallet.manage-webhooks (policies.WalletManageWebhooks) allows the caller's
// loaded membership. Wallet role owner or admin passes, and so does account
// role owner or admin. WalletContext has already loaded the wallet, so a
// missing wallet is 404 before this check. A missing webhook is left to the
// handler, which answers 404, and only a webhook that exists is 403. A
// denial is 403 with the policy message. Create writes nothing, delete
// leaves the webhook in place, and a refused test is not sent.
func WalletManageWebhooks(memberships *walletrecords.Memberships, webhooks *walletrecords.Webhooks) http.Middleware {
	if memberships == nil {
		panic("wallet manage webhooks: wallet memberships are required")
	}
	if webhooks == nil {
		panic("wallet manage webhooks: wallet webhooks are required")
	}
	exists := func(ctx http.Context, webhookID, walletID uuid.UUID) bool {
		cfg, err := webhooks.FindByIDAndWallet(ctx.Context(), webhookID, walletID)
		return err == nil && cfg != nil
	}
	return authorize(facades.Gate(), policies.AbilityWalletManageWebhooks, walletChildSubject(memberships, "webhookId", exists))
}
