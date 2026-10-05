package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletManageWebhooks refuses POST /v1/wallets/{walletId}/webhooks unless
// policies.WalletManageWebhooks allows the caller's loaded membership. Wallet
// role owner or admin passes, and so does account role owner or admin.
// WalletContext has already loaded the wallet, so a missing wallet is 404
// before this check. A denial is 403 with the policy message, and no webhook
// is written.
func WalletManageWebhooks(memberships *walletrecords.Memberships) http.Middleware {
	if memberships == nil {
		panic("wallet manage webhooks: wallet memberships are required")
	}
	return func(ctx http.Context) {
		wallet := requestctx.MustWallet(ctx)
		decision := policies.WalletManageWebhooks(walletMembership(ctx, memberships, wallet.ID))
		if !decision.Allowed() {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": decision.Message()})
			return
		}
		ctx.Request().Next()
	}
}
