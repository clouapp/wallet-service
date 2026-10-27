// Package machine holds the handlers of the machine-to-machine routes.
package machine

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
	machinerequests "github.com/macrowallets/waas/app/http/requests/machine"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/tenantprovision"
)

// TenantProvisionController creates or finds the account of a platform tenant and
// its API token. middleware.TenantProvisioningKey checks the caller first.
type TenantProvisionController struct {
	provision *tenantprovision.Service
}

// NewTenantProvisionController wires the handler; the service is required.
func NewTenantProvisionController(provision *tenantprovision.Service) *TenantProvisionController {
	if provision == nil {
		panic("tenant provision controller: service is required")
	}
	return &TenantProvisionController{provision: provision}
}

// Store is POST /internal/v1/tenants: 201 when the account is created, 200 when a
// retry finds it, 409 when the identity conflicts with another account or token.
func (c *TenantProvisionController) Store(ctx http.Context) http.Response {
	var req machinerequests.TenantProvisionRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	result, err := c.provision.Provision(ctx.Context(), req.ExternalID, req.Name, req.Environment)
	if err != nil {
		if errors.Is(err, tenantprovision.ErrConflict) {
			return responses.Fail(ctx, http.StatusConflict, responses.CodeInvalidRequest, "tenant account conflict")
		}
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInvalidRequest, "tenant provisioning failed")
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	return ctx.Response().Status(status).Json(map[string]any{
		"account_id":  result.AccountID.String(),
		"api_token":   result.APIToken,
		"environment": req.Environment,
	})
}
