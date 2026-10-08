package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
)

// Fund-moving actions. Routes alias them here so they do not import policies.
const (
	FundWithdraw        = policies.FundWithdraw
	FundSweep           = policies.FundSweep
	FundCreateWallet    = policies.FundCreateWallet
	FundGenerateAddress = policies.FundGenerateAddress
)

// RequireFundAction refuses the route unless the account role loaded by
// AccountHeader may perform the action. It is for session routes. API-token
// routes do not carry an account role; their scopes are checked separately.
func RequireFundAction(action string) http.Middleware {
	return func(ctx http.Context) {
		role := AccountRole(ctx)
		if !policies.MayPerformFundAction(role, action) {
			_ = responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "insufficient role").Abort()
			return
		}
		ctx.Request().Next()
	}
}
