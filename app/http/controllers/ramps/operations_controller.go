package ramps

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	form "github.com/macrowallets/waas/app/http/requests/ramps"
	"github.com/macrowallets/waas/app/http/responses"
	svc "github.com/macrowallets/waas/app/services/ramps"
)

type OperationsController struct{ service *svc.Service }

func NewOperationsController(s *svc.Service) *OperationsController { return &OperationsController{s} }
func (c *OperationsController) Index(ctx http.Context) http.Response {
	row := requestctx.RampCustomer(ctx)
	items, err := c.service.Attention(ctx.Context(), requestctx.MustAccount(ctx), row)
	if err != nil {
		return responses.InternalError(ctx, err)
	}
	out := make([]http.Json, 0, len(items))
	for _, item := range items {
		out = append(out, http.Json{"id": item.ID, "kind": item.Kind, "provider": item.Provider, "customer_id": row.ID, "transaction_id": item.TransactionID, "since": item.Since, "age_seconds": item.AgeSeconds, "detail": item.Detail})
	}
	return ctx.Response().Success().Json(http.Json{"data": out})
}
func (c *OperationsController) Resolve(ctx http.Context) http.Response {
	var req form.ResolutionRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	err := c.service.Resolve(ctx.Context(), requestctx.MustAccount(ctx), requestctx.MustUser(ctx), requestctx.RampCustomer(ctx), svc.Resolution{Kind: req.Kind, ID: ctx.Request().Route("id"), ProviderID: req.ProviderID, Code: req.Code, Reference: req.Reference, Note: req.Note, Abandon: req.Abandon, NotCreated: req.NotCreated}, middleware.ClientIP(ctx))
	if err != nil {
		return responses.Fail(ctx, 409, "recovery_not_resolvable", "review the provider binding, operation status and second factor")
	}
	return ctx.Response().Success().Json(http.Json{"resolved": true})
}
