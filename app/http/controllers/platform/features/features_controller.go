package features

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	featuressvc "github.com/macrowallets/waas/app/services/features"
)

// FeaturesController serves platform feature flags. Only a platform admin may
// call it. An account owner who is not in platform_admins receives 403.
type FeaturesController struct {
	features *featuressvc.Service
	accounts featuressvc.Accounts
}

// NewFeaturesController wires the platform feature-flag handlers.
func NewFeaturesController(features *featuressvc.Service, accounts featuressvc.Accounts) *FeaturesController {
	if features == nil {
		panic("platform features controller: features service is required")
	}
	if accounts == nil {
		panic("platform features controller: accounts are required")
	}
	return &FeaturesController{features: features, accounts: accounts}
}

// Index godoc
// @Summary      Platform feature flags
// @Description  Every named flag for the platform. A missing row uses the catalog default (withdrawals and sweep on). Only a platform admin may read.
// @Tags         Platform Features
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  featuressvc.List
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Router       /platform/features [get]
func (ctrl *FeaturesController) Index(ctx http.Context) http.Response {
	userID, errResp := platformCaller(ctx)
	if errResp != nil {
		return errResp
	}
	view, err := ctrl.features.ListGlobal(ctx.Context(), userID)
	if errResp := mapPlatformFeatureError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// Update godoc
// @Summary      Set one platform feature flag
// @Description  Writes one boolean and returns the stored row. Only a platform admin may write. An unknown key is 404. The next gate reads the row; nothing is cached.
// @Tags         Platform Features
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        key  path  string  true  "Feature key"
// @Success      200  {object}  featuressvc.Flag
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /platform/features/{key} [patch]
func (ctrl *FeaturesController) Update(ctx http.Context) http.Response {
	userID, errResp := platformCaller(ctx)
	if errResp != nil {
		return errResp
	}
	var path requests.FeatureKeyRequest
	path.Load(ctx)
	key := path.Key
	enabled, err := requests.AccountFeatureEnabled(ctx)
	if err != nil {
		return mapPlatformFeatureBodyError(ctx, err)
	}
	flag, err := ctrl.features.SetGlobal(ctx.Context(), userID, key, enabled)
	if errResp := mapPlatformFeatureError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, flag)
}

// ShowScope godoc
// @Summary      One account's feature flags
// @Description  S2.4 GET /v1/platform/features/{scope}/{id}. features.view is not a permission row, so a platform_admins row is the gate. Scope account returns that account's stored booleans. A missing row is the catalog default and is not inserted. Global rows are not applied. global, user, and chain are 404 before the admin check. An unknown account is 404 after the admin check.
// @Tags         Platform Features
// @Security     BearerAuth
// @Produce      json
// @Param        scope  path  string  true  "Feature scope"
// @Param        id     path  string  true  "Account UUID"
// @Success      200  {object}  featuressvc.List
// @Failure      400  {object}  responses.ErrorBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/features/{scope}/{id} [get]
func (ctrl *FeaturesController) ShowScope(ctx http.Context) http.Response {
	userID, errResp := platformCaller(ctx)
	if errResp != nil {
		return errResp
	}
	var path requests.FeatureScopeRequest
	path.Load(ctx)
	view, err := ctrl.features.ListScopedForPlatform(ctx.Context(), userID, path.Scope, path.ID, ctrl.accounts)
	if errResp := mapPlatformFeatureError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

func platformCaller(ctx http.Context) (uuid.UUID, http.Response) {
	userID := middleware.SessionUserID(ctx)
	if userID == uuid.Nil {
		return uuid.Nil, responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	return userID, nil
}

func mapPlatformFeatureBodyError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, requests.ErrAccountFeatureBodyTooLarge):
		return responses.Send(ctx, http.StatusRequestEntityTooLarge, http.Json{"error": "request body is too large"})
	case errors.Is(err, requests.ErrAccountFeatureEnabledRequired):
		return responses.Send(ctx, http.StatusUnprocessableEntity, map[string]any{
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

func mapPlatformFeatureError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, featuressvc.ErrNotFound), errors.Is(err, featuressvc.ErrScopeNotFound), errors.Is(err, featuressvc.ErrAccountNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": err.Error()})
	case errors.Is(err, featuressvc.ErrInvalidAccountID):
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
	case errors.Is(err, featuressvc.ErrPlatformForbidden):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
