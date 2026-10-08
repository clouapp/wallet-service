package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletWhitelist refuses POST /v1/wallets/{walletId}/whitelist and
// DELETE /v1/wallets/{walletId}/whitelist/{entryId} unless
// policies.WalletWhitelist allows the caller's loaded membership. Wallet role
// owner or admin passes, and so does account role owner or admin.
// WalletContext has already loaded the wallet, so a missing wallet is 404
// before this check. A missing whitelist entry is left to the handler, which
// answers 404, and only an entry that exists is 403. A denial is 403 with
// the policy message. Create writes nothing, and delete leaves the entry in
// place.
func WalletWhitelist(memberships *walletrecords.Memberships, entries *walletrecords.Whitelist) http.Middleware {
	if memberships == nil {
		panic("wallet whitelist: wallet memberships are required")
	}
	if entries == nil {
		panic("wallet whitelist: whitelist entries are required")
	}
	return func(ctx http.Context) {
		wallet := requestctx.MustWallet(ctx)
		if ctx.Request().Route("entryId") != "" {
			entryID, err := requests.RouteUUID(ctx, "entryId")
			if err != nil {
				ctx.Request().Next()
				return
			}
			entry, err := entries.FindByIDAndWallet(ctx.Context(), entryID, wallet.ID)
			if err != nil || entry == nil {
				ctx.Request().Next()
				return
			}
		}
		decision := policies.WalletWhitelist(walletMembership(ctx, memberships, wallet.ID))
		if !decision.Allowed() {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": decision.Message()})
			return
		}
		ctx.Request().Next()
	}
}
