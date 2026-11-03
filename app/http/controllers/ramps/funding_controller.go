package ramps

import (
	"errors"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	form "github.com/macrowallets/waas/app/http/requests/ramps"
	"github.com/macrowallets/waas/app/http/responses"
	auth "github.com/macrowallets/waas/app/services/auth"
	svc "github.com/macrowallets/waas/app/services/ramps"
	resource "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
)

type FundingController struct{ service *svc.Service }

func NewFundingController(s *svc.Service) *FundingController { return &FundingController{s} }
func (c *FundingController) Fund(ctx http.Context) http.Response {
	var req form.FundRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	walletID, _ := uuid.Parse(ctx.Request().Route("walletId"))
	result, err := c.service.Fund(ctx.Context(), requestctx.MustAccount(ctx), requestctx.MustUser(ctx), requestctx.RampCustomer(ctx), svc.FundInput{OrderID: ctx.Request().Route("id"), WalletID: walletID, Passphrase: req.Passphrase, Code: req.Code, ActionRevision: req.ActionRevision})
	if err != nil {
		if errors.Is(err, svc.ErrCustomerNotFound) {
			return responses.Fail(ctx, 404, "order_not_found", "order not found")
		}
		if errors.Is(err, svc.ErrFundingForbidden) {
			return responses.Fail(ctx, 403, "funding_forbidden", "funding wallet or second factor not available")
		}
		if errors.Is(err, auth.ErrInvalidSecondFactor) {
			return responses.Fail(ctx, 422, "mfa_invalid", "invalid authenticator code")
		}
		return responses.Fail(ctx, 409, "funding_not_ready", "funding could not be completed; refresh the operation")
	}
	return ctx.Response().Success().Json(resource.NewOrderResource(result))
}
