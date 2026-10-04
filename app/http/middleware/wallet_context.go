package middleware

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletContext resolves the {walletId} route parameter, loads the wallet,
// and verifies the caller is a member of the wallet or its account.
// Sets "wallet" and "wallet_id" in context for downstream handlers.
func WalletContext() http.Middleware {
	return func(ctx http.Context) {
		rawID := ctx.Request().Route("walletId")
		walletID, err := uuid.Parse(rawID)
		if err != nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "invalid wallet id"}).Abort()
			return
		}

		wallet, err := container.MustMake[*walletrecords.Wallets]().FindByID(ctx.Context(), walletID)
		if err != nil || wallet == nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "wallet not found"}).Abort()
			return
		}

		userID := contextUserID(ctx)
		accounts := container.MustMake[*accountsvc.Service]()
		var accountMember *models.AccountUser
		var account *models.Account
		if wallet.AccountID != nil {
			member, memberErr := accounts.FindMember(ctx.Context(), *wallet.AccountID, userID)
			if memberErr == nil && member != nil {
				accountMember = member
			}
			loaded, accountErr := accounts.FindByID(ctx.Context(), *wallet.AccountID)
			if accountErr == nil {
				account = loaded
			}
		}
		_, walletErr := container.MustMake[*walletrecords.Members]().FindByWalletAndUser(ctx.Context(), walletID, userID)
		if walletErr != nil && !errors.Is(walletErr, models.ErrRepositoryNotFound) {
			_ = responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch wallet"}).Abort()
			return
		}
		hasWalletMembership := walletErr == nil

		if accountMember != nil {
			viewAll := account != nil && account.ViewAllWallets
			if !policies.SeesEveryAccountWallet(accountMember.Role, viewAll) && !hasWalletMembership {
				_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "wallet not found"}).Abort()
				return
			}
		} else if !hasWalletMembership {
			_ = responses.Send(ctx, http.StatusForbidden, http.Json{"error": "not a member of this wallet or its account"}).Abort()
			return
		}

		if wallet.AccountID != nil && !abortUnlessAccountAllows(ctx, account) {
			return
		}

		ctx.WithValue(requestctx.KeyWallet, wallet)
		ctx.WithValue(requestctx.KeyWalletID, wallet.ID)
		ctx.Request().Next()
	}
}
