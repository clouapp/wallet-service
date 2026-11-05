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

// PayoutAddressController serves the standing USD payout addresses of the
// customer X-Xip-Customer-Id names.
type PayoutAddressController struct {
	service *clientramps.LiquidationAddressService
}

// NewPayoutAddressController wires the controller.
func NewPayoutAddressController(service *clientramps.LiquidationAddressService) *PayoutAddressController {
	return &PayoutAddressController{service: service}
}

// Provision godoc
//
//	@Summary		Provision the standing USD payout address
//	@Description	Returns the address that pays the user's saved US bank account over ACH or wire, creating it on the first call: every deposit of the token on the chain into it becomes a USD payout to the account. Answers 201 when it was created and 200 when it already existed, so a retry is safe; a different reference or wallet on an existing address is written through (the wallet is where the provider returns a deposit it cannot pay out). Only the person's own customer has USD payout addresses: acting for a company answers business_payout_unsupported. min_amount is in min_amount_currency and applies after fee_percent, because the provider keeps a smaller deposit. Send amount (the token amount about to go out) right before the transfer: below the minimum the answer is 422 amount_below_minimum with limit_usd, and nothing should be sent.
//	@Tags			offramps
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		rampsrequests.ProvisionPayoutAddressRequest	true	"The bank account, rail and token"
//	@Success		201		{object}	resources.PayoutAddressResource
//	@Success		200		{object}	resources.PayoutAddressResource
//	@Failure		404		{object}	responses.ErrorResponse				"not_found: no such bank account"
//	@Failure		409		{object}	responses.ErrorResponse				"integration_required, kyc_pending, kyc_rejected, rail_not_eligible or provisioning_in_flight"
//	@Failure		422		{object}	responses.ValidationErrorResponse	"validation failed; bank_account_rail_unsupported (the provider said the account does not take the rail); business_payout_unsupported (the customer acted for is a company)"
//	@Failure		401		{object}	responses.ErrorResponse				"unauthenticated: the account was deleted while the request ran"
//	@Failure		500		{object}	responses.ErrorResponse
//	@Failure		503		{object}	responses.ErrorResponse	"rail_disabled or no_provider_available"
//	@Router			/api/offramps/address [post]
func (c *PayoutAddressController) Provision(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ProvisionPayoutAddressRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, created, err := c.service.Provision(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), dto.ProvisionPayoutAddressDTO{
		Rail: req.Rail, BankAccountID: req.BankAccountID,
		Token: req.Token, Chain: req.Chain, Wallet: req.Wallet, Reference: req.Reference,
		Amount: req.Amount,
	})
	if err != nil {
		return mapError(ctx, err, "provision payout address")
	}
	status := contractshttp.StatusOK
	if created {
		status = contractshttp.StatusCreated
	}
	return ctx.Response().Status(status).Json(resources.NewPayoutAddressResource(view))
}

// SimulateDeposit godoc
//
//	@Summary		Simulate a deposit into the USD payout address (sandbox only)
//	@Description	Asks the provider's sandbox to pretend the user sent amount of the token to their payout address, and returns the off-ramp order the drain opened. No on-chain transfer happens.
//	@Tags			offramps
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		rampsrequests.SimulatePayoutDepositRequest	true	"The address route and the amount"
//	@Success		200		{object}	resources.OrderResource
//	@Failure		404		{object}	responses.ErrorResponse				"not_found: no such bank account, or no active payout address for the route"
//	@Failure		409		{object}	responses.ErrorResponse				"sandbox_only"
//	@Failure		422		{object}	responses.ValidationErrorResponse	"validation failed, or business_payout_unsupported (the customer acted for is a company)"
//	@Failure		500		{object}	responses.ErrorResponse
//	@Failure		503		{object}	responses.ErrorResponse	"rail_disabled"
//	@Router			/api/offramps/address/simulate-deposit [post]
func (c *PayoutAddressController) SimulateDeposit(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.SimulatePayoutDepositRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.SimulateDeposit(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), dto.SimulatePayoutDepositDTO{
		Rail: req.Rail, BankAccountID: req.BankAccountID, Token: req.Token, Chain: req.Chain, Amount: req.Amount,
	})
	if err != nil {
		return mapError(ctx, err, "simulate payout deposit")
	}
	return ctx.Response().Success().Json(resources.NewOrderResource(view))
}
