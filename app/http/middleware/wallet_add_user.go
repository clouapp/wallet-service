package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletAddUser refuses POST /v1/wallets/{walletId}/users unless the Gate's
// wallet.add-user (policies.WalletAddUser) allows the caller's loaded
// membership. Wallet role owner or admin passes, and so does account role
// owner or admin. WalletContext has already loaded the wallet, so a missing
// wallet is 404 before this check. A denial is 403 with the policy message.
func WalletAddUser(memberships *walletrecords.Memberships) http.Middleware {
	if memberships == nil {
		panic("wallet add user: wallet memberships are required")
	}
	return authorize(facades.Gate(), policies.AbilityWalletAddUser, walletSubject(memberships))
}
