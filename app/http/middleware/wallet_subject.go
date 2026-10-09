package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// walletSubject hands the Gate the caller's membership on the wallet
// WalletContext loaded.
func walletSubject(memberships *walletrecords.Memberships) subject {
	return func(ctx http.Context) (map[string]any, outcome) {
		wallet := requestctx.MustWallet(ctx)
		return walletArguments(walletMembership(ctx, memberships, wallet.ID)), decide
	}
}

// walletChildSubject resolves the child the path names under param before
// the membership: a malformed child, or one exists does not find on the
// wallet, is left to the handler, which answers 400 or 404, so only a child
// that exists is 403. A route without param goes straight to the membership.
func walletChildSubject(memberships *walletrecords.Memberships, param string, exists func(ctx http.Context, childID, walletID uuid.UUID) bool) subject {
	membership := walletSubject(memberships)
	return func(ctx http.Context) (map[string]any, outcome) {
		wallet := requestctx.MustWallet(ctx)
		if ctx.Request().Route(param) != "" {
			childID, err := requests.RouteUUID(ctx, param)
			if err != nil || !exists(ctx, childID, wallet.ID) {
				return nil, pass
			}
		}
		return membership(ctx)
	}
}

// walletArguments are the wallet abilities' arguments for one membership.
func walletArguments(membership policies.WalletMembership) map[string]any {
	return map[string]any{
		policies.ArgWalletRole:  membership.WalletRole,
		policies.ArgAccountRole: membership.AccountRole,
		policies.ArgUserID:      membership.UserID,
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
