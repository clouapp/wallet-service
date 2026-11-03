package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/middleware"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	rampsrequests "github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/ramps"
	resources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	integrationsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/integration"
)

// IntegrationController exposes the user's provider links.
type IntegrationController struct {
	service *integrationsvc.Service
}

// NewIntegrationController wires the controller.
func NewIntegrationController(service *integrationsvc.Service) *IntegrationController {
	return &IntegrationController{service: service}
}

// Index godoc
//
//	@Summary		List provider integrations
//	@Description	Every provider the user has been linked to, and whether the provider-side customer exists yet.
//	@Tags			integrations
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Success		200					{array}		resources.IntegrationResource
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/integrations [get]
func (c *IntegrationController) Index(ctx contractshttp.Context) contractshttp.Response {
	customerID := middleware.CurrentCustomer(ctx).ID
	rows, err := c.service.List(customerID)
	if err != nil {
		return mapError(ctx, err, "list integrations")
	}
	return ctx.Response().Success().Json(resources.NewIntegrationCollection(rows))
}

// Show godoc
//
//	@Summary	Get one provider integration
//	@Tags		integrations
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		provider			path		string	true	"Provider code"
//	@Success	200					{object}	resources.IntegrationResource
//	@Failure	404					{object}	responses.ErrorResponse
//	@Failure	422					{object}	responses.ValidationErrorResponse
//	@Failure	500					{object}	responses.ErrorResponse
//	@Router		/api/integrations/{provider} [get]
func (c *IntegrationController) Show(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ProviderPathRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	customerID := middleware.CurrentCustomer(ctx).ID

	// Refresh reconciles against the provider, which is what makes a link the
	// user disabled on the provider's side visible here.
	row, err := c.service.Refresh(ctx, customerID, req.Provider)
	if err != nil {
		return mapError(ctx, err, "show integration")
	}
	return ctx.Response().Success().Json(resources.NewIntegrationResource(row))
}

// Start godoc
//
//	@Summary		Start a provider integration
//	@Description	Idempotent: a second call returns the existing link rather than opening another customer upstream.
//	@Tags			integrations
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			provider			path		string	true	"Provider code"
//	@Success		201					{object}	resources.IntegrationResource
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		503					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		409					{object}	responses.ErrorResponse	"customer_closed: the customer X-Xip-Customer-Id names is closed"
//	@Router			/api/integrations/{provider}/start [post]
func (c *IntegrationController) Start(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ProviderPathRequest
	if response := requests.ValidatePath(ctx, &req); response != nil {
		return response
	}
	customerID := middleware.CurrentCustomer(ctx).ID
	row, err := c.service.Start(ctx, customerID, req.Provider)
	if err != nil {
		return mapError(ctx, err, "start integration")
	}
	return ctx.Response().Status(contractshttp.StatusCreated).
		Json(resources.NewIntegrationResource(row))
}

// Refresh godoc
//
//	@Summary	Refresh a provider integration
//	@Tags		integrations
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		provider			path		string	true	"Provider code"
//	@Success	200					{object}	resources.IntegrationResource
//	@Failure	409					{object}	responses.ErrorResponse
//	@Failure	422					{object}	responses.ValidationErrorResponse
//	@Failure	500					{object}	responses.ErrorResponse
//	@Failure	404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router		/api/integrations/{provider}/refresh [post]
func (c *IntegrationController) Refresh(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ProviderPathRequest
	if response := requests.ValidatePath(ctx, &req); response != nil {
		return response
	}
	customerID := middleware.CurrentCustomer(ctx).ID
	row, err := c.service.Refresh(ctx, customerID, req.Provider)
	if err != nil {
		return mapError(ctx, err, "refresh integration")
	}
	return ctx.Response().Success().Json(resources.NewIntegrationResource(row))
}
