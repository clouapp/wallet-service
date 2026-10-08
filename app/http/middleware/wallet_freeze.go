package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletFreeze refuses POST /v1/wallets/{walletId}/freeze unless the Gate's
// wallet.freeze (policies.WalletFreeze) allows the caller's loaded
// membership. Wallet role owner passes, and so does account role owner or
// admin. A wallet admin does not. WalletContext has already loaded the
// wallet, so a caller who cannot see it — including an account user with
// view_all_wallets false and no wallet membership — is 404 before this
// check. A denial is 403 with the policy message, and the wallet stays
// unfrozen.
func WalletFreeze(memberships *walletrecords.Memberships) http.Middleware {
	if memberships == nil {
		panic("wallet freeze: wallet memberships are required")
	}
	return authorize(facades.Gate(), policies.AbilityWalletFreeze, walletSubject(memberships))
}
