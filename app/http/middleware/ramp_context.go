package middleware

import (
	"context"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/ramps"
	engine "github.com/macrowallets/waas/packages/rampengine/app/http/middleware"
	"strings"
)

const (
	PermRampCustomersRead   = policies.PermRampCustomersRead
	PermRampCustomersWrite  = policies.PermRampCustomersWrite
	PermRampComplianceRead  = policies.PermRampComplianceRead
	PermRampProfileRead     = policies.PermRampProfileRead
	PermRampComplianceWrite = policies.PermRampComplianceWrite
	PermRampOrdersRead      = policies.PermRampOrdersRead
	PermRampOrdersCreate    = policies.PermRampOrdersCreate
)

func RampCustomerContext(service *ramps.Service) http.Middleware {
	return func(ctx http.Context) {
		id, err := uuid.Parse(ctx.Request().Route("customerId"))
		if err != nil {
			_ = responses.Fail(ctx, 404, "customer_not_found", "customer not found").Abort()
			return
		}
		scope, err := service.Customer(ctx.Context(), requestctx.MustAccount(ctx), id)
		if err != nil {
			_ = responses.Fail(ctx, 404, "customer_not_found", "customer not found").Abort()
			return
		}
		engine.BindMacroCustomer(ctx, scope.Contact, scope.Holder, scope.Member)
		ctx.WithContext(context.WithValue(ctx.Context(), requestctx.RampCustomerKey{}, scope.Public))
		ctx.Request().Next()
	}
}
func RampSessionPermission(permission string) http.Middleware {
	return func(ctx http.Context) {
		role, _ := requestctx.AccountRole(ctx)
		if !policies.MayOperateRamps(role, permission) {
			_ = responses.Fail(ctx, 403, "forbidden", "forbidden").Abort()
			return
		}
		ctx.WithContext(context.WithValue(ctx.Context(), requestctx.RampActorKey{}, requestctx.MustUser(ctx)))
		ctx.Request().Next()
	}
}

// Financial-compliance scopes require an explicit grant, including on legacy tokens.
func RampTokenPermission(permission string) http.Middleware {
	return func(ctx http.Context) {
		token, ok := requestctx.APIToken(ctx)
		if !ok || token == nil || strings.TrimSpace(token.Permissions) == "" || !policies.APITokenAllows(token.Permissions, permission) {
			_ = responses.Fail(ctx, 403, "forbidden", "explicit ramp permission required").Abort()
			return
		}
		ctx.Request().Next()
	}
}
func RampNewOperations(service *ramps.Service) http.Middleware {
	return func(ctx http.Context) {
		row := requestctx.RampCustomer(ctx)
		if row == nil || service.CheckNewOperations(ctx.Context(), row.AccountID, row.Relationship) != nil {
			_ = responses.Fail(ctx, 403, "ramp_operations_paused", "ramp operations paused").Abort()
			return
		}
		ctx.Request().Next()
	}
}

func RampProfileAudit(service *ramps.Service) http.Middleware {
	return func(ctx http.Context) {
		id := uuid.Nil
		if user, ok := requestctx.User(ctx); ok {
			id = user.ID
		}
		if token, ok := requestctx.APIToken(ctx); ok {
			id = token.ID
		}
		if id == uuid.Nil || service.AuditPrivateRead(ctx.Context(), id, requestctx.RampCustomer(ctx), ClientIP(ctx)) != nil {
			_ = responses.Fail(ctx, 500, "internal_error", "profile audit unavailable").Abort()
			return
		}
		ctx.Request().Next()
	}
}

func RampIntegrationsEnabled(service *ramps.Service) http.Middleware {
	return func(ctx http.Context) {
		enabled, err := service.IntegrationsEnabled(ctx.Context(), requestctx.MustAccount(ctx).ID)
		if err != nil || !enabled {
			_ = responses.Fail(ctx, 403, "ramp_integrations_disabled", "ramp integrations are disabled").Abort()
			return
		}
		ctx.Request().Next()
	}
}
