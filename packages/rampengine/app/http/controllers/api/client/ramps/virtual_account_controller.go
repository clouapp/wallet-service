package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/middleware"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	rampsrequests "github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/ramps"
	resources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

// VirtualAccountController serves the standing deposit account of the customer the
// request acts for: the permanent, amount-less payment target the PIX
// on-ramp collects through.
type VirtualAccountController struct {
	service *clientramps.VirtualAccountService
}

// NewVirtualAccountController wires the controller.
func NewVirtualAccountController(service *clientramps.VirtualAccountService) *VirtualAccountController {
	return &VirtualAccountController{service: service}
}

// Provision godoc
//
//	@Summary		Provision the standing deposit account
//	@Description	Returns the permanent deposit account of the customer the request acts for, for source_currency (brl when absent, or usd), creating it on the first call. Answers 201 when it was created and 200 when it already existed, so a retry is safe. The deposit instructions carry no amount and never change: a br_code for brl, US bank details (bank_name, bank_routing_number, bank_account_number, bank_beneficiary_name, …) for usd. The user pays them as often as they like, and each deposit becomes its own on-ramp order.
//	@Tags			onramps
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string											false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.ProvisionVirtualAccountRequest	true	"Where deposits are delivered"
//	@Success		201					{object}	resources.VirtualAccountResource
//	@Success		200					{object}	resources.VirtualAccountResource
//	@Failure		401					{object}	responses.ErrorResponse				"unauthenticated: the account was deleted while this request was running"
//	@Failure		409					{object}	responses.ErrorResponse				"integration_required, kyc_pending, rail_not_eligible (the tax identity and residence the user declared do not open the deposit; names no provider), or provisioning_in_flight; customer_closed: the customer the header names is closed (it reads, it moves no money)"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; wallet_not_registered: a company's wallet is not one it registered (POST /api/wallets)"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		503					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/onramps/account [post]
func (c *VirtualAccountController) Provision(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ProvisionVirtualAccountRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, created, err := c.service.Provision(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), dto.ProvisionVirtualAccountDTO{
		SourceCurrency: sourceCurrencyOf(req.SourceCurrency),
		Token:          req.Token, Chain: req.Chain, Wallet: req.Wallet,
	})
	if err != nil {
		return mapError(ctx, err, "provision virtual account")
	}
	status := contractshttp.StatusOK
	if created {
		status = contractshttp.StatusCreated
	}
	return ctx.Response().Status(status).Json(resources.NewVirtualAccountResource(view))
}

// Show godoc
//
//	@Summary		Get the standing deposit account
//	@Description	The account is a profile-shaped object, not an order: it is read as-is and not refreshed against the provider. A customer that has never provisioned one in that currency gets 404.
//	@Tags			onramps
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			source_currency		query		string	false	"brl (default) or usd"
//	@Success		200					{object}	resources.VirtualAccountResource
//	@Failure		404					{object}	responses.ErrorResponse	"not_found: the customer holds no standing account; customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Router			/api/onramps/account [get]
func (c *VirtualAccountController) Show(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ShowVirtualAccountRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.Show(middleware.CurrentCustomer(ctx).ID, sourceCurrencyOf(req.SourceCurrency))
	if err != nil {
		return mapError(ctx, err, "show virtual account")
	}
	return ctx.Response().Success().Json(resources.NewVirtualAccountResource(view))
}

// UpdateDestination godoc
//
//	@Summary		Change where deposits are delivered
//	@Description	Points the standing account at another wallet. The deposit code is unaffected — the user keeps the one saved in their bank app. Requires an MFA code when the user has MFA enabled: this redirects every future deposit with no signal at the bank app.
//	@Tags			onramps
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string										false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.UpdateVirtualAccountRequest	true	"New destination"
//	@Success		200					{object}	resources.VirtualAccountResource
//	@Failure		403					{object}	responses.ErrorResponse				"mfa_required"
//	@Failure		404					{object}	responses.ErrorResponse				"not_found: the customer holds no standing account; customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; wallet_not_registered: a company's wallet is not one it registered (POST /api/wallets)"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		409					{object}	responses.ErrorResponse	"customer_closed: the customer the header names is closed (it reads, it moves no money)"
//	@Router			/api/onramps/account [put]
func (c *VirtualAccountController) UpdateDestination(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.UpdateVirtualAccountRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.UpdateDestination(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), dto.UpdateVirtualAccountDTO{
		ProvisionVirtualAccountDTO: dto.ProvisionVirtualAccountDTO{
			SourceCurrency: sourceCurrencyOf(req.SourceCurrency),
			Token:          req.Token, Chain: req.Chain, Wallet: req.Wallet,
		},
		Code:     req.Code,
		CodeType: req.CodeType,
	})
	if err != nil {
		return mapError(ctx, err, "update virtual account")
	}
	return ctx.Response().Success().Json(resources.NewVirtualAccountResource(view))
}

// sourceCurrencyOf is the currency a request names, or brl when it names none:
// every installed build asks for its BRL account without saying so.
func sourceCurrencyOf(requested string) string {
	if requested == "" {
		return clientramps.ClientSourceCurrency
	}
	return requested
}
