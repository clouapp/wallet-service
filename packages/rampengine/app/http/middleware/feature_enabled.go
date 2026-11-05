package middleware

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
)

// AdminFeature returns a middleware that refuses the route unless the named
// feature flag is active for the signed-in admin. Must be chained AFTER
// AdminAuth.
//
// It answers 404 rather than 403 on purpose. A withdrawn feature should look
// like a feature that was never built: 403 says "this exists and you may not
// use it", which tells an admin there is something to ask for and tells an
// attacker the route is worth revisiting. This is availability, not permission
// — HasPermission is the one that means "you specifically may not".
//
// The flag read fails *closed* here, unlike the login path. Refusing an
// enrollment screen during a flag-store outage costs an admin a few minutes;
// serving it would let them set up a factor the system may be in the middle of
// withdrawing.
//
// The two directions meet in one corner: during an outage, an admin whose flag
// was withdrawn while an enrollment stayed behind is still challenged by the
// authenticator (login falls back to the enrollment alone) and cannot reach
// /mfa/disable to clear it. That corner is accepted rather than designed away.
// It needs three conditions at once, it lasts only as long as the outage, and a
// recovery code still finishes the login — so nobody is locked out, they are
// asked for a factor they still hold.
func AdminFeature(feature string) contractshttp.Middleware {
	features := facades.Feature()
	return func(ctx contractshttp.Context) {
		admin := CurrentAdmin(ctx)
		if admin == nil {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "unauthorized").Abort()
			return
		}
		active, err := features.IsActive(ctx.Context(), feature, admin)
		if err != nil {
			facades.Log().WithContext(ctx).Errorf("feature %q for admin %d: %v (refusing)", feature, admin.ID, err)
			responses.Error(ctx, contractshttp.StatusNotFound, "not found").Abort()
			return
		}
		if !active {
			responses.Error(ctx, contractshttp.StatusNotFound, "not found").Abort()
			return
		}
		ctx.Request().Next()
	}
}

// Feature is AdminFeature for the client: it refuses the route unless the
// named feature flag is active for the signed-in user. Must be chained AFTER
// Auth.
//
// The refusal is 404 {"error":"not_found"}, the code the client contract reads
// (the ramps' own "not found" of an order), so an app hides what the flag
// gates — a withdrawn feature looks like one never built, for the reasons
// AdminFeature gives. The read fails closed for the same reason: a feature
// served during a flag-store outage would be served to whoever it was never
// turned on for.
func Feature(feature string) contractshttp.Middleware {
	features := facades.Feature()
	return func(ctx contractshttp.Context) {
		user := CurrentUser(ctx)
		if user == nil {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "unauthenticated").Abort()
			return
		}
		active, err := features.IsActive(ctx.Context(), feature, user)
		if err != nil {
			facades.Log().WithContext(ctx).Errorf("feature %q for user %d: %v (refusing)", feature, user.ID, err)
		}
		if err != nil || !active {
			responses.Error(ctx, contractshttp.StatusNotFound, "not_found").Abort()
			return
		}
		ctx.Request().Next()
	}
}
