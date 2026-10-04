package features

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	featuressvc "github.com/macrowallets/waas/app/services/features"
)

// FeaturesController serves account feature flags on the dashboard.
type FeaturesController struct {
	features *featuressvc.Service
}

// NewFeaturesController wires the dashboard account feature-flag handlers.
func NewFeaturesController(features *featuressvc.Service) *FeaturesController {
	if features == nil {
		panic("dashboard account features controller: features service is required")
	}
	return &FeaturesController{features: features}
}

// Index godoc
// @Summary      Account feature flags
// @Description  Every named flag for one account. A missing row uses the catalog default (withdrawals and sweep on). Members with settings.view may read.
// @Tags         Account Features
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200  {object}  featuressvc.List
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/features [get]
func (ctrl *FeaturesController) Index(ctx http.Context) http.Response {
	account, role, errResp := accountCaller(ctx)
	if errResp != nil {
		return errResp
	}
	view, err := ctrl.features.List(ctx.Context(), account.ID, role)
	if errResp := mapFeatureError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

func accountCaller(ctx http.Context) (*models.Account, string, http.Response) {
	account, _ := requestctx.Account(ctx)
	if account == nil {
		return nil, "", responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
	role, _ := requestctx.AccountRole(ctx)
	return account, role, nil
}

func mapFeatureError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, featuressvc.ErrNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "feature not found"})
	case errors.Is(err, featuressvc.ErrViewForbidden),
		errors.Is(err, featuressvc.ErrUpdateForbidden):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
