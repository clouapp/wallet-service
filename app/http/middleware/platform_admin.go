package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
)

// PlatformAdminLookup is the platform_admins read PlatformAdmin needs.
type PlatformAdminLookup interface {
	IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error)
}

// platformForbiddenFallback is the sentence for a route the refusal table does
// not name. tests/feature/api/platform/guard fails until every route is named.
const platformForbiddenFallback = "you do not have permission to access platform administration"

// PlatformAdmin refuses a signed-in caller who has no platform_admins row, before
// any handler resolves the target of the request. Without it a handler that
// looks its target up first answers 404 to a non-admin, which tells a member
// which accounts, users, chains and settings groups exist. SessionAuth runs
// before it.
//
// The services keep their own admin check (defence in depth, and the call
// path that is not HTTP). refusal returns the sentence the service would have
// answered for the matched route, so a refused caller gets the same 403 body as
// before; it receives the method and the route pattern, "GET" and
// "/v1/platform/features/{scope}/{id}", with HEAD reported as GET.
func PlatformAdmin(admins PlatformAdminLookup, refusal func(method, pattern string) string) contractshttp.Middleware {
	if admins == nil {
		panic("platform admin middleware: admin lookup is required")
	}
	return func(ctx contractshttp.Context) {
		userID, ok := requestctx.UserID(ctx)
		if !ok {
			_ = responses.Send(ctx, http.StatusUnauthorized, contractshttp.Json{"error": "user not found"}).Abort()
			return
		}
		admin, err := admins.IsPlatformAdmin(ctx.Context(), userID)
		if err != nil {
			_ = responses.InternalError(ctx, err).Abort()
			return
		}
		if admin {
			ctx.Request().Next()
			return
		}
		_ = responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, refusalFor(ctx, refusal)).Abort()
	}
}

func refusalFor(ctx contractshttp.Context, refusal func(method, pattern string) string) string {
	method := strings.ToUpper(ctx.Request().Method())
	if method == http.MethodHead {
		method = http.MethodGet
	}
	if refusal != nil {
		if message := refusal(method, ctx.Request().OriginPath()); message != "" {
			return message
		}
	}
	return platformForbiddenFallback
}
