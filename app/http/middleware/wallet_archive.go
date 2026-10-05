package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletArchive refuses POST /v1/wallets/{walletId}/archive unless
// policies.WalletArchive allows the caller's loaded membership. Wallet role
// owner or admin passes, and so does account role owner or admin.
// WalletContext has already loaded the wallet, so a caller who cannot see it
// — including an account user with view_all_wallets false and no wallet
// membership — is 404 before this check. A denial is 403 with the policy
// message, and the wallet stays unarchived.
func WalletArchive(memberships *walletrecords.Memberships) http.Middleware {
	if memberships == nil {
		panic("wallet archive: wallet memberships are required")
	}
	return func(ctx http.Context) {
		wallet := requestctx.MustWallet(ctx)
		decision := policies.WalletArchive(walletMembership(ctx, memberships, wallet.ID))
		if !decision.Allowed() {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": decision.Message()})
			return
		}
		ctx.Request().Next()
	}
}
