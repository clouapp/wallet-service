package routes

import (
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers/machine"
	"github.com/macrowallets/waas/app/http/middleware"
)

// RegisterTenantProvisioning exposes only the idempotent account operation to
// the platform's dedicated provisioning key. It is outside account API auth.
func RegisterTenantProvisioning() {
	facades.Route().Prefix("/internal/v1").Middleware(
		middleware.Throttle(middleware.ThrottleAPI),
		middleware.CacheControl(0),
	).Post("/tenants", (machine.TenantProvisionController{}).Store)
}
