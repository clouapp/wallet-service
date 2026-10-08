package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletManageWebhooks refuses POST /v1/wallets/{walletId}/webhooks,
// POST /v1/wallets/{walletId}/webhooks/{webhookId}/test, and
// DELETE /v1/wallets/{walletId}/webhooks/{webhookId} unless
// policies.WalletManageWebhooks allows the caller's loaded membership. Wallet
// role owner or admin passes, and so does account role owner or admin.
// WalletContext has already loaded the wallet, so a missing wallet is 404
// before this check. A missing webhook is left to the handler, which answers
// 404, and only a webhook that exists is 403. A denial is 403 with the
// policy message. Create writes nothing, delete leaves the webhook in place,
// and a refused test is not sent.
func WalletManageWebhooks(memberships *walletrecords.Memberships, webhooks *walletrecords.Webhooks) http.Middleware {
	if memberships == nil {
		panic("wallet manage webhooks: wallet memberships are required")
	}
	if webhooks == nil {
		panic("wallet manage webhooks: wallet webhooks are required")
	}
	return func(ctx http.Context) {
		wallet := requestctx.MustWallet(ctx)
		if ctx.Request().Route("webhookId") != "" {
			webhookID, err := requests.RouteUUID(ctx, "webhookId")
			if err != nil {
				ctx.Request().Next()
				return
			}
			cfg, err := webhooks.FindByIDAndWallet(ctx.Context(), webhookID, wallet.ID)
			if err != nil || cfg == nil {
				ctx.Request().Next()
				return
			}
		}
		decision := policies.WalletManageWebhooks(walletMembership(ctx, memberships, wallet.ID))
		if !decision.Allowed() {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": decision.Message()})
			return
		}
		ctx.Request().Next()
	}
}
