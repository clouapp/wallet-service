package middleware

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/container"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
	"github.com/macrowallets/waas/packages/rampengine/app/policies"
)

// HasPermission returns a middleware that asserts the current admin has the
// named permission. Must be chained AFTER AdminAuth.
func HasPermission(permission string) contractshttp.Middleware {
	policy := container.MustMake[*policies.AdminPolicy](container.AdminPolicyKey)
	return func(ctx contractshttp.Context) {
		admin := CurrentAdmin(ctx)
		if admin == nil {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "unauthorized").Abort()
			return
		}
		if !policy.Can(admin, permission) {
			responses.Error(ctx, contractshttp.StatusForbidden, "forbidden").Abort()
			return
		}
		ctx.Request().Next()
	}
}
