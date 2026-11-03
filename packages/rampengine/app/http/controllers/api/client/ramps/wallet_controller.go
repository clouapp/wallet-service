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

// WalletController registers the addresses a customer holds as its own.
type WalletController struct {
	service *clientramps.WalletService
}

// NewWalletController wires the controller.
func NewWalletController(service *clientramps.WalletService) *WalletController {
	return &WalletController{service: service}
}

// Store godoc
//
//	@Summary		Register a wallet of the customer
//	@Description	Registers the address as the company's own, for the business the X-Xip-Customer-Id header names: a company's deposits arrive only to, and its withdrawals leave only from, a registered address. A person registers none: without the header, or naming the user's own person, it is 403 forbidden. Answers under data, 201 when registered and 200 when the address was already this customer's. A company must be approved at a provider and active first: until then 409 kyc_pending (with provider and pending_steps when one provider alone serves it) or kyc_rejected when every provider refused it for good, before any MFA attempt is spent; an approved draft is activated by the registration itself. Requires an MFA code when the user has MFA enabled, as changing the deposit account does. An address another customer registered, or another customer's deposit account delivers to, is 409 wallet_in_use.
//	@Tags			wallets
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string								false	"The business to register for; without it the user's own person, which is refused"
//	@Param			request				body		rampsrequests.RegisterWalletRequest	true	"The address and the step-up code"
//	@Success		201					{object}	resources.WalletResource			"under data"
//	@Success		200					{object}	resources.WalletResource			"under data: already this customer's"
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		403					{object}	responses.ErrorResponse				"forbidden: a person registers no wallet; or mfa_required"
//	@Failure		404					{object}	responses.ErrorResponse				"customer_not_found"
//	@Failure		409					{object}	responses.ErrorResponse				"kyc_pending, kyc_rejected, customer_closed or wallet_in_use"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed, or mfa_invalid"
//	@Failure		429					{object}	responses.ErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Router			/api/wallets [post]
func (c *WalletController) Store(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.RegisterWalletRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	wallet, created, err := c.service.Register(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx),
		dto.RegisterWalletDTO{Chain: req.Chain, Address: req.Address, Code: req.Code, CodeType: req.CodeType})
	if err != nil {
		return mapError(ctx, err, "register wallet")
	}
	status := contractshttp.StatusOK
	if created {
		status = contractshttp.StatusCreated
	}
	return ctx.Response().Status(status).Json(contractshttp.Json{"data": resources.NewWalletResource(wallet)})
}
