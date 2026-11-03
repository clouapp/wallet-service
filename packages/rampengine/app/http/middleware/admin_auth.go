package middleware

import (
	"context"
	"strings"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/container"
	auditdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/audit"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// adminKey is unexported so that AdminAuth is the only code that can put an
// admin in a request context: whatever CurrentAdmin returns has passed the
// token check.
type adminKey struct{}

func withAdmin(ctx context.Context, admin *models.Admin) context.Context {
	return context.WithValue(ctx, adminKey{}, admin)
}

// AdminAuth pins authentication to the admin JWT guard. On success the
// resolved *models.Admin is attached to the request context, where controllers
// read it through CurrentAdmin. A token issued before the admin's sessions were
// revoked, or in that second (see identity.AcceptsTokenIssuedAt), is refused
// like any token that no longer holds. /admin/api/refresh sits behind this
// middleware, so such a token cannot buy a new one either.
func AdminAuth() contractshttp.Middleware {
	admins := container.MustMake[repositories.AdminRepository](container.AdminRepositoryKey)
	return func(ctx contractshttp.Context) {
		header := ctx.Request().Header("Authorization", "")
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if token == "" {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "missing authorization token").Abort()
			return
		}
		guard := facades.Auth(ctx).Guard(identity.GuardAdmin)
		payload, err := guard.Parse(token)
		if err != nil || payload.Guard != identity.GuardAdmin {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "invalid or expired token").Abort()
			return
		}
		id, err := guard.ID()
		adminID, ok := requests.ParseID(id)
		if err != nil || !ok {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "invalid token subject").Abort()
			return
		}
		admin, err := admins.FindByID(adminID)
		if err != nil {
			facades.Log().WithContext(ctx).Errorf("admin auth: find admin %d: %v", adminID, err)
			responses.Error(ctx, contractshttp.StatusInternalServerError, "internal server error").Abort()
			return
		}
		if admin == nil {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "admin not found").Abort()
			return
		}
		if !admin.AcceptsTokenIssuedAt(payload.IssuedAt) {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "invalid or expired token").Abort()
			return
		}
		ctx.WithContext(withAdmin(ctx.Context(), admin))
		ctx.Request().Next()
	}
}

// CurrentAdmin is the admin AdminAuth authenticated. Every route behind
// AdminAuth has one; it is nil only on a route AdminAuth does not guard.
func CurrentAdmin(ctx contractshttp.Context) *models.Admin {
	admin, _ := ctx.Context().Value(adminKey{}).(*models.Admin)
	return admin
}

// CurrentActor is who an admin act is recorded against: the admin AdminAuth
// authenticated, and the address the request came from as ClientIP resolved
// it. A controller hands it to the service that performs the act.
func CurrentActor(ctx contractshttp.Context) auditdto.ActorDTO {
	return auditdto.ActorDTO{Admin: CurrentAdmin(ctx), IP: ctx.Request().Ip()}
}
