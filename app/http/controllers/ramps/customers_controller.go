package ramps

import (
	"errors"
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	form "github.com/macrowallets/waas/app/http/requests/ramps"
	resource "github.com/macrowallets/waas/app/http/resources/ramps"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/ramps"
	engineResource "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	"strconv"
)

type CustomersController struct{ service *ramps.Service }

func NewCustomersController(s *ramps.Service) *CustomersController { return &CustomersController{s} }
func (c *CustomersController) Index(ctx http.Context) http.Response {
	page, _ := strconv.Atoi(ctx.Request().Query("page", "1"))
	limit, _ := strconv.Atoi(ctx.Request().Query("limit", "25"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	rows, total, err := c.service.Customers(ctx.Context(), requestctx.MustAccount(ctx), page, limit, ctx.Request().Query("search"))
	if err != nil {
		return responses.InternalError(ctx, err)
	}
	items := make([]resource.Customer, 0, len(rows))
	for i := range rows {
		items = append(items, resource.NewCustomer(&rows[i]))
	}
	return ctx.Response().Success().Json(http.Json{"data": items, "meta": http.Json{"page": page, "limit": limit, "total": total}})
}
func (c *CustomersController) Store(ctx http.Context) http.Response {
	var req form.CreateCustomerRequest
	if reply := requests.Validate(ctx, &req); reply != nil {
		return reply
	}
	row, created, err := c.service.CreateCustomer(ctx.Context(), requestctx.MustAccount(ctx), req.Draft())
	if err != nil {
		if errors.Is(err, ramps.ErrCustomerConflict) {
			return responses.Fail(ctx, 409, "customer_conflict", "external reference already belongs to a different holder")
		}
		if errors.Is(err, ramps.ErrOperationsPaused) {
			return responses.Fail(ctx, 403, "ramp_operations_paused", "ramp operations paused")
		}
		return responses.InternalError(ctx, err)
	}
	status := 200
	if created {
		status = 201
	}
	return ctx.Response().Status(status).Json(http.Json{"data": resource.NewCustomer(row)})
}
func (c *CustomersController) Show(ctx http.Context) http.Response {
	return ctx.Response().Success().Json(http.Json{"data": resource.NewCustomer(requestctx.RampCustomer(ctx))})
}
func (c *CustomersController) AccountHolder(ctx http.Context) http.Response {
	row, err := c.service.AccountHolder(ctx.Context(), requestctx.MustAccount(ctx))
	if err != nil {
		if errors.Is(err, ramps.ErrCustomerNotFound) {
			return responses.Fail(ctx, 404, "customer_not_found", "account holder not registered")
		}
		return responses.InternalError(ctx, err)
	}
	return ctx.Response().Success().Json(http.Json{"data": resource.NewCustomer(row)})
}

func (c *CustomersController) BusinessProfile(ctx http.Context) http.Response {
	view, err := c.service.BusinessProfile(ctx.Context(), requestctx.RampCustomer(ctx))
	if err != nil {
		if errors.Is(err, ramps.ErrCustomerNotFound) {
			return responses.Fail(ctx, 404, "customer_not_found", "business not found")
		}
		return responses.InternalError(ctx, err)
	}
	if view == nil {
		return ctx.Response().NoContent(204)
	}
	return ctx.Response().Success().Json(resource.NewBusinessProfile(view))
}
func (c *CustomersController) Features(ctx http.Context) http.Response {
	rows, err := c.service.CustomerFeatures(ctx.Context(), requestctx.RampCustomer(ctx))
	if err != nil {
		return responses.InternalError(ctx, err)
	}
	out := make([]resource.Feature, 0, len(rows))
	for _, row := range rows {
		out = append(out, resource.Feature{Key: row.Key, Label: row.Label, Effective: row.Effective, Override: row.Override})
	}
	return ctx.Response().Success().Json(http.Json{"data": out})
}
func (c *CustomersController) SetFeature(ctx http.Context) http.Response {
	var req form.FeatureRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	if err := c.service.SetCustomerFeature(ctx.Context(), requestctx.MustUserID(ctx), requestctx.RampCustomer(ctx), ctx.Request().Route("feature"), *req.Enabled); err != nil {
		return responses.Fail(ctx, 422, "feature_not_available", "feature not available")
	}
	return c.Features(ctx)
}
func (c *CustomersController) Wallets(ctx http.Context) http.Response {
	wallets, err := c.service.CustomerWallets(ctx.Context(), requestctx.RampCustomer(ctx))
	if err != nil {
		return responses.InternalError(ctx, err)
	}
	return ctx.Response().Success().Json(http.Json{"data": engineResource.NewWalletCollection(wallets)})
}

func (c *CustomersController) Status(ctx http.Context) http.Response {
	enabled, err := c.service.IntegrationsEnabled(ctx.Context(), requestctx.MustAccount(ctx).ID)
	if err != nil {
		return responses.InternalError(ctx, err)
	}
	return ctx.Response().Success().Json(http.Json{"enabled": enabled})
}
