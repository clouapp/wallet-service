package routes

import (
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers/machine"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/services/tenantprovision"
)

// RegisterTenantProvisioning exposes only the idempotent account operation to
// the platform's dedicated provisioning key. It is outside account API auth.
func RegisterTenantProvisioning() {
	controller := machine.NewTenantProvisionController(container.MustMake[*tenantprovision.Service]())
	facades.Route().Prefix("/internal/v1").Middleware(
		middleware.Throttle(middleware.ThrottleAPI),
		middleware.TenantProvisioningKey(facades.Config().GetString("vault.tenant_provisioning_key")),
		middleware.CacheControl(0),
	).Post("/tenants", controller.Store)
}
