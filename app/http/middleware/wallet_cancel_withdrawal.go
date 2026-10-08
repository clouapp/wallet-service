package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// WalletCancelWithdrawal refuses POST
// /v1/wallets/{walletId}/withdrawals/{withdrawalId}/cancel unless the Gate's
// wallet.cancel-withdrawal (policies.WalletCancelWithdrawal) allows the
// caller's loaded membership and the withdrawal's creator. A non-auditor
// creator may cancel their own withdrawal. Wallet role owner or admin may
// cancel any, and so may account role owner or admin. An account auditor is
// denied even when they created the withdrawal. WalletContext has already
// loaded the wallet, so a caller who cannot see it — including an account
// user with view_all_wallets false and no wallet membership — is 404 before
// this check. A missing withdrawal is left to the handler, which answers 404
// before anything is cancelled. A denial is 403 with the policy message, and
// the withdrawal is not cancelled.
func WalletCancelWithdrawal(memberships *walletrecords.Memberships, withdrawals *withdrawalrecords.Records) http.Middleware {
	if memberships == nil {
		panic("wallet cancel withdrawal: wallet memberships are required")
	}
	if withdrawals == nil {
		panic("wallet cancel withdrawal: withdrawals are required")
	}
	return authorize(facades.Gate(), policies.AbilityWalletCancelWithdrawal, func(ctx http.Context) (map[string]any, outcome) {
		wallet := requestctx.MustWallet(ctx)
		withdrawalID, err := requests.RouteUUID(ctx, "withdrawalId")
		if err != nil {
			return nil, pass
		}
		withdrawal, err := withdrawals.FindByIDAndWallet(ctx.Context(), withdrawalID, wallet.ID)
		if err != nil || withdrawal == nil {
			return nil, pass
		}
		creatorID := uuid.Nil
		if withdrawal.CreatedBy != nil {
			creatorID = *withdrawal.CreatedBy
		}
		arguments := walletArguments(walletMembership(ctx, memberships, wallet.ID))
		arguments[policies.ArgCreatorID] = creatorID
		return arguments, decide
	})
}
