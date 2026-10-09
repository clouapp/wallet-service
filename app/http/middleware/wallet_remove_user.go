package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletRemoveUser refuses DELETE /v1/wallets/{walletId}/users/{userId}
// unless the Gate's wallet.remove-user (policies.WalletRemoveUser) allows
// the caller's loaded membership. Wallet role owner or admin passes, and so
// does account role owner or admin. WalletContext has already loaded the
// wallet, so a missing wallet is 404 before this check. A denial is 403 with
// the policy message, and the membership stays.
func WalletRemoveUser(memberships *walletrecords.Memberships) http.Middleware {
	if memberships == nil {
		panic("wallet remove user: wallet memberships are required")
	}
	return authorize(facades.Gate(), policies.AbilityWalletRemoveUser, walletSubject(memberships))
}
