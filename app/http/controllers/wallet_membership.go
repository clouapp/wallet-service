package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletMembership is the caller's wallet and account roles for one wallet.
// A user id the session did not store yields an empty membership, the same
// deny the policy returned when the context value was missing.
func WalletMembership(ctx http.Context, memberships *walletrecords.Memberships, walletID uuid.UUID) policies.WalletMembership {
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
