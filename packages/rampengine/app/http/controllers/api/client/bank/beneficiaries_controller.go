package bank

import (
	"errors"
	"fmt"

	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/bank"
	"github.com/macrowallets/waas/packages/rampengine/app/http/middleware"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	bankrequests "github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/bank"
	resources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/bank"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
	banksvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/bank"
)

// BeneficiariesController is the customer's US bank account book, under the
// customer the request acts for (CustomerScope).
type BeneficiariesController struct {
	service *banksvc.Service
}

// NewBeneficiariesController wires the controller.
func NewBeneficiariesController(service *banksvc.Service) *BeneficiariesController {
	return &BeneficiariesController{service: service}
}

// pathExternalID reads the {id} segment. Its shape is the repository's to
// judge: an id that is no UUID answers 404 like one that names nothing.
func pathExternalID(ctx contractshttp.Context) (string, contractshttp.Response) {
	var req requests.PathExternalID
	if response := requests.ValidatePath(ctx, &req); response != nil {
		return "", response
	}
	return req.ID, nil
}

func input(req *bankrequests.BeneficiaryRequest) dto.BeneficiaryDTO {
	return dto.BeneficiaryDTO{
		Label: req.Label, AccountClass: req.AccountClass, AccountType: req.AccountType, Relationship: req.Relationship,
		BeneficiaryName: req.BeneficiaryName, RoutingNumber: req.RoutingNumber, AccountNumber: req.AccountNumber,
		AddressLine1: req.AddressLine1, AddressLine2: req.AddressLine2, City: req.City,
		StateCode: req.StateCode, PostalCode: req.PostalCode, BusinessIndustry: req.BusinessIndustry,
	}
}

func mapError(ctx contractshttp.Context, err error, action string) contractshttp.Response {
	var fe *banksvc.FieldError
	switch {
	case errors.As(err, &fe):
		return responses.FieldError(ctx, fe.Field, fe.Rule, fe.Message)
	case errors.Is(err, banksvc.ErrBankBeneficiaryNotFound):
		return responses.Error(ctx, contractshttp.StatusNotFound, "not_found")
	case errors.Is(err, banksvc.ErrBankBeneficiaryDuplicate):
		return responses.Error(ctx, contractshttp.StatusUnprocessableEntity, "bank_beneficiary_duplicate")
	}
	return responses.InternalError(ctx, fmt.Errorf("bank beneficiaries: %s: %w", action, err))
}

// Index godoc
//
//	@Summary	List the customer's saved US bank accounts
//	@Tags		bank-beneficiaries
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Success	200					{array}		resources.BeneficiarySummary
//	@Failure	401					{object}	responses.ErrorResponse
//	@Failure	404					{object}	responses.ErrorResponse	"customer_not_found"
//	@Failure	500					{object}	responses.ErrorResponse
//	@Router		/api/offramps/bank/beneficiaries [get]
func (c *BeneficiariesController) Index(ctx contractshttp.Context) contractshttp.Response {
	list, err := c.service.List(middleware.CurrentCustomer(ctx).ID)
	if err != nil {
		return mapError(ctx, err, "list")
	}
	return ctx.Response().Success().Json(resources.NewBeneficiaryCollection(list))
}

// Show godoc
//
//	@Summary	Show one saved US bank account, numbers masked
//	@Tags		bank-beneficiaries
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		id					path		string	true	"Beneficiary id"
//	@Success	200					{object}	resources.BeneficiaryDetail
//	@Failure	401					{object}	responses.ErrorResponse
//	@Failure	404					{object}	responses.ErrorResponse	"not_found, or customer_not_found"
//	@Failure	500					{object}	responses.ErrorResponse
//	@Router		/api/offramps/bank/beneficiaries/{id} [get]
func (c *BeneficiariesController) Show(ctx contractshttp.Context) contractshttp.Response {
	id, response := pathExternalID(ctx)
	if response != nil {
		return response
	}
	rec, err := c.service.Get(middleware.CurrentCustomer(ctx).ID, id)
	if err != nil {
		return mapError(ctx, err, "show")
	}
	return ctx.Response().Success().Json(resources.NewBeneficiaryDetail(rec))
}

// Store godoc
//
//	@Summary	Save a US bank account for bank payouts
//	@Tags		bank-beneficiaries
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string							false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		request				body		bankrequests.BeneficiaryRequest	true	"Bank account"
//	@Success	201					{object}	resources.BeneficiaryDetail
//	@Failure	401					{object}	responses.ErrorResponse
//	@Failure	404					{object}	responses.ErrorResponse	"customer_not_found"
//	@Failure	422					{object}	responses.ErrorResponse	"bank_beneficiary_duplicate, or a ValidationErrorResponse"
//	@Failure	500					{object}	responses.ErrorResponse
//	@Router		/api/offramps/bank/beneficiaries [post]
func (c *BeneficiariesController) Store(ctx contractshttp.Context) contractshttp.Response {
	var req bankrequests.BeneficiaryRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	rec, err := c.service.Create(middleware.CurrentCustomer(ctx).ID, input(&req))
	if err != nil {
		return mapError(ctx, err, "create")
	}
	return ctx.Response().Status(contractshttp.StatusCreated).Json(resources.NewBeneficiaryDetail(rec))
}

// Update godoc
//
//	@Summary	Replace a saved US bank account; bumps its revision when bank data changed
//	@Tags		bank-beneficiaries
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string							false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		id					path		string							true	"Beneficiary id"
//	@Param		request				body		bankrequests.BeneficiaryRequest	true	"Bank account"
//	@Success	200					{object}	resources.BeneficiaryDetail
//	@Failure	401					{object}	responses.ErrorResponse
//	@Failure	404					{object}	responses.ErrorResponse	"not_found, or customer_not_found"
//	@Failure	422					{object}	responses.ErrorResponse	"bank_beneficiary_duplicate, or a ValidationErrorResponse"
//	@Failure	500					{object}	responses.ErrorResponse
//	@Router		/api/offramps/bank/beneficiaries/{id} [put]
func (c *BeneficiariesController) Update(ctx contractshttp.Context) contractshttp.Response {
	id, response := pathExternalID(ctx)
	if response != nil {
		return response
	}
	var req bankrequests.BeneficiaryRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	rec, err := c.service.Update(middleware.CurrentCustomer(ctx).ID, id, input(&req))
	if err != nil {
		return mapError(ctx, err, "update")
	}
	return ctx.Response().Success().Json(resources.NewBeneficiaryDetail(rec))
}

// Destroy godoc
//
//	@Summary	Delete a saved US bank account (past transactions keep their snapshot)
//	@Tags		bank-beneficiaries
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header	string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		id					path	string	true	"Beneficiary id"
//	@Success	204
//	@Failure	401	{object}	responses.ErrorResponse
//	@Failure	404	{object}	responses.ErrorResponse	"not_found, or customer_not_found"
//	@Failure	500	{object}	responses.ErrorResponse
//	@Router		/api/offramps/bank/beneficiaries/{id} [delete]
func (c *BeneficiariesController) Destroy(ctx contractshttp.Context) contractshttp.Response {
	id, response := pathExternalID(ctx)
	if response != nil {
		return response
	}
	if err := c.service.Delete(middleware.CurrentCustomer(ctx).ID, id); err != nil {
		return mapError(ctx, err, "delete")
	}
	return ctx.Response().NoContent(contractshttp.StatusNoContent)
}
