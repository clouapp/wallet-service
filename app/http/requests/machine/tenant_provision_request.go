// Package machine holds the form requests of the machine-to-machine routes.
package machine

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
)

// TenantProvisionRequest is the body of POST /internal/v1/tenants: the platform's
// stable id of the tenant, its account name and environment. The provisioning key
// is checked before (middleware.TenantProvisioningKey).
type TenantProvisionRequest struct {
	requests.Open
	ExternalID  string `form:"external_id" json:"external_id"`
	Name        string `form:"name"        json:"name"`
	Environment string `form:"environment" json:"environment"`
}

func (r *TenantProvisionRequest) Filters(http.Context) map[string]string {
	return map[string]string{"external_id": "trim", "name": "trim"}
}

func (r *TenantProvisionRequest) Rules(http.Context) map[string]string {
	return map[string]string{
		"external_id": "required|max_len:128",
		"name":        "required|max_len:255",
		"environment": "required|in:" + models.EnvironmentTest + "," + models.EnvironmentProd,
	}
}

func (r *TenantProvisionRequest) Messages(http.Context) map[string]string {
	return map[string]string{
		"external_id.required": "external_id is required",
		"external_id.max_len":  "external_id must be at most 128 characters",
		"name.required":        "name is required",
		"name.max_len":         "name must be at most 255 characters",
		"environment.required": "environment is required",
		"environment.in":       "environment must be test or prod",
	}
}
