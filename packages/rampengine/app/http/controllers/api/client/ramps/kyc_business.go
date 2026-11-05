package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/middleware"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	rampsrequests "github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/ramps"
	resources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// actsForBusiness reports whether the request acts for a business: the KYC
// submission routes then take the company's bodies (API contract §5, §6) and
// answer in the same shapes as a person's.
func actsForBusiness(ctx contractshttp.Context) bool {
	return middleware.CurrentCustomer(ctx).Type == models.CustomerTypeBusiness
}

// submitBusinessApplication is SubmitApplication for a business.
func (c *KycController) submitBusinessApplication(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.SubmitBusinessApplicationRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	input := req.Input()
	input.SignedAgreementID = req.SignedAgreementID
	row, err := c.service.SubmitBusinessApplication(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx),
		req.Provider, input)
	if err != nil {
		return mapError(ctx, err, "submit business application")
	}
	return ctx.Response().Status(contractshttp.StatusAccepted).Json(resources.NewKycResource(row))
}

// submitBusinessApplicationFanOut is SubmitApplicationFanOut for a business.
func (c *KycController) submitBusinessApplicationFanOut(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.SubmitBusinessApplicationFanOutRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	outcomes, err := c.service.SubmitBusinessApplicationToEnabled(ctx, middleware.CurrentUser(ctx),
		middleware.CurrentCustomer(ctx), req.Input())
	if err != nil && len(outcomes) == 0 {
		return mapError(ctx, err, "submit business application fan-out")
	}
	return fanOutResponse(ctx, outcomes, err, "submit business application fan-out")
}

// submitBusinessDocuments is SubmitDocuments for a business.
func (c *KycController) submitBusinessDocuments(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.SubmitBusinessDocumentsRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	row, err := c.service.SubmitBusinessDocuments(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx),
		req.Provider, req.Input())
	if err != nil {
		return mapError(ctx, err, "submit business documents")
	}
	return ctx.Response().Status(contractshttp.StatusAccepted).Json(resources.NewKycResource(row))
}

// submitBusinessDocumentsFanOut is SubmitDocumentsFanOut for a business.
func (c *KycController) submitBusinessDocumentsFanOut(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.SubmitBusinessDocumentsFanOutRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	outcomes, err := c.service.SubmitBusinessDocumentsToEnabled(ctx, middleware.CurrentUser(ctx),
		middleware.CurrentCustomer(ctx), req.Input())
	if err != nil && len(outcomes) == 0 {
		return mapError(ctx, err, "submit business documents fan-out")
	}
	return fanOutResponse(ctx, outcomes, err, "submit business documents fan-out")
}
