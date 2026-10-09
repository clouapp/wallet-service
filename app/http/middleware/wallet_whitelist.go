package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletWhitelist refuses POST /v1/wallets/{walletId}/whitelist and
// DELETE /v1/wallets/{walletId}/whitelist/{entryId} unless the Gate's
// wallet.whitelist (policies.WalletWhitelist) allows the caller's loaded
// membership. Wallet role owner or admin passes, and so does account role
// owner or admin. WalletContext has already loaded the wallet, so a missing
// wallet is 404 before this check. A missing whitelist entry is left to the
// handler, which answers 404, and only an entry that exists is 403. A denial
// is 403 with the policy message. Create writes nothing, and delete leaves
// the entry in place.
func WalletWhitelist(memberships *walletrecords.Memberships, entries *walletrecords.Whitelist) http.Middleware {
	if memberships == nil {
		panic("wallet whitelist: wallet memberships are required")
	}
	if entries == nil {
		panic("wallet whitelist: whitelist entries are required")
	}
	exists := func(ctx http.Context, entryID, walletID uuid.UUID) bool {
		entry, err := entries.FindByIDAndWallet(ctx.Context(), entryID, walletID)
		return err == nil && entry != nil
	}
	return authorize(facades.Gate(), policies.AbilityWalletWhitelist, walletChildSubject(memberships, "entryId", exists))
}
