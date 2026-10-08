package middleware

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WalletContextDeps is what WalletContext reads per request.
type WalletContextDeps struct {
	Wallets  *walletrecords.Wallets
	Accounts *accountsvc.Service
	Members  *walletrecords.Members
}

// WalletContext resolves the {walletId} route parameter, loads the wallet,
// and verifies the caller is a member of the wallet or its account.
// Sets "wallet" and "wallet_id" in context for downstream handlers.
func WalletContext(deps WalletContextDeps) http.Middleware {
	if deps.Wallets == nil || deps.Accounts == nil || deps.Members == nil {
		panic("wallet context: wallets, accounts and members are required")
	}
	wallets, accounts, members := deps.Wallets, deps.Accounts, deps.Members
	return func(ctx http.Context) {
		rawID := ctx.Request().Route("walletId")
		walletID, err := uuid.Parse(rawID)
		if err != nil {
			_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "invalid wallet id").Abort()
			return
		}

		wallet, err := wallets.FindByID(ctx.Context(), walletID)
		if err != nil || wallet == nil {
			_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "wallet not found").Abort()
			return
		}

		userID := contextUserID(ctx)
		var accountMember *models.AccountUser
		var account *models.Account
		if wallet.AccountID != nil {
			member, memberErr := accounts.FindMember(ctx.Context(), *wallet.AccountID, userID)
			if memberErr != nil && !errors.Is(memberErr, models.ErrRepositoryNotFound) {
				_ = responses.Fail(ctx, http.StatusServiceUnavailable, responses.CodeUnavailable, "failed to load membership").Abort()
				return
			}
			if memberErr == nil && member != nil {
				accountMember = member
			}
			loaded, accountErr := accounts.FindByID(ctx.Context(), *wallet.AccountID)
			if accountErr == nil {
				account = loaded
			}
		}
		_, walletErr := members.FindByWalletAndUser(ctx.Context(), walletID, userID)
		if walletErr != nil && !errors.Is(walletErr, models.ErrRepositoryNotFound) {
			_ = responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch wallet").Abort()
			return
		}
		hasWalletMembership := walletErr == nil

		if accountMember != nil {
			viewAll := account != nil && account.ViewAllWallets
			if !policies.SeesEveryAccountWallet(accountMember.Role, viewAll) && !hasWalletMembership {
				_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "wallet not found").Abort()
				return
			}
		} else if !hasWalletMembership {
			_ = responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "not a member of this wallet or its account").Abort()
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
