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
	accounts featuressvc.Accounts
}

// FeaturesControllerDeps is everything the platform features controller needs.
type FeaturesControllerDeps struct {
	Features *featuressvc.Service
	Accounts featuressvc.Accounts
}

// NewFeaturesController wires the platform feature-flag handlers from FeaturesControllerDeps.
func NewFeaturesController(deps FeaturesControllerDeps) *FeaturesController {
	if deps.Features == nil {
		panic("platform features controller: features service is required")
	}
	if deps.Accounts == nil {
		panic("platform features controller: accounts are required")
	}
	return &FeaturesController{features: deps.Features, accounts: deps.Accounts}
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

// UpdateScope godoc
// @Summary      Set account feature flags
// @Description  S2.4 PUT /v1/platform/features/{scope}/{id}. FeaturePolicy names features.update for any scope and features.account.update for the account scope. Neither is a permission row, so a platform_admins row is the gate and stands in for both. The pair is not a second gate. The body is {"features":[{"key","enabled"}]}. Every key is checked before the first write. An unknown key stores nothing. global, user, and chain are 404 before the admin check. An unknown account is 404 after the admin check. A closed global row is not applied and is not written. Omitted flags are not inserted.
// @Tags         Platform Features
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        scope  path  string  true  "Feature scope"
// @Param        id     path  string  true  "Account UUID"
// @Success      200  {object}  featuressvc.List
// @Failure      400  {object}  responses.ErrorBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /platform/features/{scope}/{id} [put]
func (ctrl *FeaturesController) UpdateScope(ctx http.Context) http.Response {
	var path requests.FeatureScopeRequest
	path.Load(ctx)
	return ctrl.writeScope(ctx, path.Scope, path.ID, "", false)
}

// UpdateScopeFeature godoc
// @Summary      Set one account feature flag
// @Description  S2.4 PUT /v1/platform/features/{scope}/{id}/{feature}. FeaturePolicy names features.update for any scope and features.account.update for the account scope. Neither is a permission row, so a platform_admins row is the gate and stands in for both. The pair is not a second gate. The body is {"enabled":bool}. An unknown key stores nothing. global, user, and chain are 404 before the admin check. An unknown account is 404 after the admin check. A closed global row is not applied and is not written.
// @Tags         Platform Features
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        scope    path  string  true  "Feature scope"
// @Param        id       path  string  true  "Account UUID"
// @Param        feature  path  string  true  "Feature key"
// @Success      200  {object}  featuressvc.Flag
// @Failure      400  {object}  responses.ErrorBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /platform/features/{scope}/{id}/{feature} [put]
func (ctrl *FeaturesController) UpdateScopeFeature(ctx http.Context) http.Response {
	var path requests.FeatureScopeFeatureRequest
	path.Load(ctx)
	return ctrl.writeScope(ctx, path.Scope, path.ID, path.Feature, true)
}

func (ctrl *FeaturesController) writeScope(ctx http.Context, scope, id, feature string, single bool) http.Response {
	userID, errResp := platformCaller(ctx)
	if errResp != nil {
		return errResp
	}
	if strings.TrimSpace(scope) != featuressvc.ScopeAccount {
		return mapPlatformFeatureError(ctx, featuressvc.ErrScopeNotFound)
	}
	accountID, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil || accountID == uuid.Nil {
		return mapPlatformFeatureError(ctx, featuressvc.ErrInvalidAccountID)
	}

	var writes []featuressvc.ScopedWrite
	if single {
		enabled, err := requests.AccountFeatureEnabled(ctx)
		if err != nil {
			return mapPlatformFeatureBodyError(ctx, err)
		}
		writes = []featuressvc.ScopedWrite{{Key: feature, Enabled: enabled}}
	} else {
		parsed, err := requests.PlatformFeatureScopeWrites(ctx)
		if err != nil {
			return mapPlatformFeatureScopeBodyError(ctx, err)
		}
		writes = make([]featuressvc.ScopedWrite, 0, len(parsed))
		for _, write := range parsed {
			writes = append(writes, featuressvc.ScopedWrite{Key: write.Key, Enabled: write.Enabled})
		}
	}

	view, err := ctrl.features.SetScopedForPlatform(ctx.Context(), userID, scope, id, writes, ctrl.accounts)
	if errResp := mapPlatformFeatureError(ctx, err); errResp != nil {
		return errResp
	}
	if single {
		if len(view.Features) != 1 {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
		}
		return responses.Send(ctx, http.StatusOK, view.Features[0])
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

func mapPlatformFeatureScopeBodyError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, requests.ErrPlatformFeatureScopeBodyTooLarge):
		return responses.Send(ctx, http.StatusRequestEntityTooLarge, http.Json{"error": "request body is too large"})
	case errors.Is(err, requests.ErrPlatformFeatureScopeFeaturesRequired):
		return responses.FieldsFailed(ctx, map[string][]string{
			"features": {"features is required"},
		})
	case errors.Is(err, requests.ErrPlatformFeatureScopeDuplicate):
		return responses.FieldsFailed(ctx, map[string][]string{
			"features": {"feature key is duplicated"},
		})
	default:
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
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
	case errors.Is(err, featuressvc.ErrDuplicateWrite):
		return responses.FieldsFailed(ctx, map[string][]string{
			"features": {"feature key is duplicated"},
		})
	case errors.Is(err, featuressvc.ErrPlatformForbidden):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
