package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletAddUser refuses POST /v1/wallets/{walletId}/users unless
// policies.WalletAddUser allows the caller's loaded membership. Wallet role
// owner or admin passes, and so does account role owner or admin.
// WalletContext has already loaded the wallet, so a missing wallet is 404
// before this check. A denial is 403 with the policy message.
func WalletAddUser(memberships *walletrecords.Memberships) http.Middleware {
	if memberships == nil {
		panic("wallet add user: wallet memberships are required")
	}
	return func(ctx http.Context) {
		wallet := requestctx.MustWallet(ctx)
		decision := policies.WalletAddUser(walletMembership(ctx, memberships, wallet.ID))
		if !decision.Allowed() {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": decision.Message()})
			return
		}
		ctx.Request().Next()
	}
}

// walletMembership is the caller's wallet and account roles for one wallet.
// A user id the session did not store yields an empty membership, the same
// deny the policy returned when the context value was missing.
func walletMembership(ctx http.Context, memberships *walletrecords.Memberships, walletID uuid.UUID) policies.WalletMembership {
	userID, userOK := requestctx.UserID(ctx)
	if !userOK || memberships == nil {
		return policies.WalletMembership{}
	}
	walletRole, accountRole := memberships.ForWallet(ctx, walletID, userID)
	return policies.WalletMembership{
		UserID:      userID,
		WalletRole:  walletRole,
		AccountRole: accountRole,
	}
}
