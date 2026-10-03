package features

import (
	"errors"
	"strings"

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
}

// NewFeaturesController wires the platform feature-flag handlers.
func NewFeaturesController(features *featuressvc.Service) *FeaturesController {
	if features == nil {
		panic("platform features controller: features service is required")
	}
	return &FeaturesController{features: features}
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
	return ctx.Response().Json(http.StatusOK, view)
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
	key := strings.TrimSpace(ctx.Request().Route("key"))
	enabled, err := requests.AccountFeatureEnabled(ctx)
	if err != nil {
		return mapPlatformFeatureBodyError(ctx, err)
	}
	flag, err := ctrl.features.SetGlobal(ctx.Context(), userID, key, enabled)
	if errResp := mapPlatformFeatureError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Json(http.StatusOK, flag)
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

func mapPlatformFeatureError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, featuressvc.ErrNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "feature not found"})
	case errors.Is(err, featuressvc.ErrPlatformForbidden):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
