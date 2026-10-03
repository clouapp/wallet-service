package features

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/requests"
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
	return ctx.Response().Json(http.StatusOK, view)
}

// Update godoc
// @Summary      Set one account feature flag
// @Description  Writes one boolean and returns the stored row. An unknown key is 404. Auditor and user receive 403 and the row is unchanged.
// @Tags         Account Features
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Param        key        path  string  true  "Feature key"
// @Success      200  {object}  featuressvc.Flag
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/features/{key} [patch]
func (ctrl *FeaturesController) Update(ctx http.Context) http.Response {
	account, role, errResp := accountCaller(ctx)
	if errResp != nil {
		return errResp
	}
	var path requests.FeatureKeyRequest
	path.Load(ctx)
	key := path.Key
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}
	enabled, err := requests.AccountFeatureEnabled(ctx)
	if err != nil {
		return mapFeatureBodyError(ctx, err)
	}
	flag, err := ctrl.features.Set(ctx.Context(), account.ID, actorID, role, key, enabled)
	if errResp := mapFeatureError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Json(http.StatusOK, flag)
}

func accountCaller(ctx http.Context) (*models.Account, string, http.Response) {
	account, _ := ctx.Value("account").(*models.Account)
	if account == nil {
		return nil, "", responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
	role, _ := ctx.Value("account_role").(string)
	return account, role, nil
}

func mapFeatureBodyError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, requests.ErrAccountFeatureBodyTooLarge):
		return responses.Send(ctx, http.StatusRequestEntityTooLarge, http.Json{"error": "request body is too large"})
	case errors.Is(err, requests.ErrAccountFeatureEnabledRequired):
		return ctx.Response().Json(http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{
				"code":    responses.CodeValidationFailed,
				"message": "validation failed",
			},
			"errors": map[string][]string{
				"enabled": {"enabled is required"},
			},
		})
	default:
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
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
