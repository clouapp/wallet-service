package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletUpdate refuses PATCH /v1/wallets/{walletId}/settings unless the
// Gate's wallet.update (policies.WalletUpdate) allows the caller's loaded
// membership. Wallet role owner or admin passes, and so does account role
// owner or admin. WalletContext has already loaded the wallet, so a caller
// who cannot see it is 404 before this check. A denial is 403 with the
// policy message, before the body is read, and the settings stay unchanged.
func WalletUpdate(memberships *walletrecords.Memberships) http.Middleware {
	if memberships == nil {
		panic("wallet update: wallet memberships are required")
	}
	return authorize(facades.Gate(), policies.AbilityWalletUpdate, walletSubject(memberships))
}
