package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/middleware"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	rampsrequests "github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/ramps"
	resources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

// BankAccountController serves the US bank accounts a user saves to receive
// USD payouts.
type BankAccountController struct {
	service *clientramps.BankAccountService
}

// NewBankAccountController wires the controller.
func NewBankAccountController(service *clientramps.BankAccountService) *BankAccountController {
	return &BankAccountController{service: service}
}

// bankAccountID validates the {id} segment. The one value the numeric rule
// lets through that ParseID refuses is past uint64, which names no account.
func bankAccountID(ctx contractshttp.Context) (uint, contractshttp.Response) {
	var req requests.PathID
	if response := requests.ValidatePath(ctx, &req); response != nil {
		return 0, response
	}
	id, ok := requests.ParseID(req.ID)
	if !ok {
		return 0, responses.Error(ctx, contractshttp.StatusNotFound, "not_found")
	}
	return id, nil
}

// Index godoc
//
//	@Summary	List the saved US bank accounts
//	@Tags		bank-accounts
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	resources.BankAccountCollection
//	@Failure	401	{object}	responses.ErrorResponse
//	@Failure	500	{object}	responses.ErrorResponse
//	@Router		/api/bank-accounts [get]
func (c *BankAccountController) Index(ctx contractshttp.Context) contractshttp.Response {
	views, err := c.service.List(middleware.CurrentUser(ctx).ID)
	if err != nil {
		return mapError(ctx, err, "list bank accounts")
	}
	return ctx.Response().Success().Json(resources.NewBankAccountCollection(views))
}

// Store godoc
//
//	@Summary		Save a US bank account
//	@Description	Saves an account in the United States to receive USD payouts by ACH or wire. Requires an MFA code when the user has MFA enabled: this is where a new payout destination enters. The numbers come back only by their last four digits.
//	@Tags			bank-accounts
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		rampsrequests.CreateBankAccountRequest	true	"Bank account"
//	@Success		201		{object}	resources.BankAccountResource
//	@Failure		401		{object}	responses.ErrorResponse
//	@Failure		403		{object}	responses.ErrorResponse				"mfa_required"
//	@Failure		409		{object}	responses.ErrorResponse				"bank_account_already_exists"
//	@Failure		422		{object}	responses.ValidationErrorResponse	"a failed rule, or mfa_invalid"
//	@Failure		500		{object}	responses.ErrorResponse
//	@Router			/api/bank-accounts [post]
func (c *BankAccountController) Store(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.CreateBankAccountRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.Create(ctx, middleware.CurrentUser(ctx), dto.CreateBankAccountDTO{
		Label: req.Label, OwnerName: req.AccountOwnerName, BankName: req.BankName,
		RoutingNumber: req.RoutingNumber, AccountNumber: req.AccountNumber, AccountType: req.AccountType,
		Address: models.BankAccountAddress{
			StreetLine1: req.Address.StreetLine1, StreetLine2: req.Address.StreetLine2,
			City: req.Address.City, State: req.Address.State,
			PostalCode: req.Address.PostalCode, Country: req.Address.Country,
		},
		Code: req.Code, CodeType: req.CodeType,
	})
	if err != nil {
		return mapError(ctx, err, "create bank account")
	}
	return ctx.Response().Status(contractshttp.StatusCreated).Json(resources.NewBankAccountResource(view))
}

// Update godoc
//
//	@Summary	Rename a saved US bank account
//	@Tags		bank-accounts
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id		path		int										true	"Bank account id"
//	@Param		request	body		rampsrequests.UpdateBankAccountRequest	true	"New label"
//	@Success	200		{object}	resources.BankAccountResource
//	@Failure	401		{object}	responses.ErrorResponse
//	@Failure	404		{object}	responses.ErrorResponse
//	@Failure	422		{object}	responses.ValidationErrorResponse
//	@Failure	500		{object}	responses.ErrorResponse
//	@Router		/api/bank-accounts/{id} [put]
func (c *BankAccountController) Update(ctx contractshttp.Context) contractshttp.Response {
	id, response := bankAccountID(ctx)
	if response != nil {
		return response
	}
	var req rampsrequests.UpdateBankAccountRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.UpdateLabel(middleware.CurrentUser(ctx).ID, id, req.Label)
	if err != nil {
		return mapError(ctx, err, "update bank account")
	}
	return ctx.Response().Success().Json(resources.NewBankAccountResource(view))
}

// Destroy godoc
//
//	@Summary	Delete a saved US bank account
//	@Tags		bank-accounts
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path	int	true	"Bank account id"
//	@Success	204
//	@Failure	401	{object}	responses.ErrorResponse
//	@Failure	404	{object}	responses.ErrorResponse
//	@Failure	422	{object}	responses.ValidationErrorResponse
//	@Failure	500	{object}	responses.ErrorResponse
//	@Router		/api/bank-accounts/{id} [delete]
func (c *BankAccountController) Destroy(ctx contractshttp.Context) contractshttp.Response {
	id, response := bankAccountID(ctx)
	if response != nil {
		return response
	}
	if err := c.service.Delete(ctx, middleware.CurrentUser(ctx).ID, id); err != nil {
		return mapError(ctx, err, "delete bank account")
	}
	return ctx.Response().NoContent(contractshttp.StatusNoContent)
}
