package settings

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// SettingsController serves the account settings registry on the dashboard.
type SettingsController struct {
	settings *settingssvc.Service
}

// NewSettingsController wires the dashboard account settings handlers.
func NewSettingsController(settings *settingssvc.Service) *SettingsController {
	if settings == nil {
		panic("dashboard account settings controller: settings service is required")
	}
	return &SettingsController{settings: settings}
}

// Show godoc
// @Summary      Account settings registry
// @Description  Sections, blocks and groups for one account. A secret is never returned; the field carries is_set.
// @Tags         Account Settings
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200  {object}  settingssvc.RegistryView
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/settings [get]
func (ctrl *SettingsController) Show(ctx http.Context) http.Response {
	account, role, errResp := accountCaller(ctx)
	if errResp != nil {
		return errResp
	}
	view, err := ctrl.settings.Registry(ctx.Context(), account.ID, role)
	if errResp := mapSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// ShowGroup godoc
// @Summary      Read one account settings group
// @Description  GET /v1/accounts/{accountId}/settings/{group} settings.read. The route applies policies.MayViewSettings before the handler. Owner, admin, and auditor may read, including a platform-managed group. A user may not, and that refusal does not return the group. A member who may read still gets 404 for an unknown group and a platform-only group. The account guard is not a second gate. A secret is never returned. The read writes no activity and does not return another account's rows.
// @Tags         Account Settings
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Param        group      path  string  true  "Settings group"
// @Success      200  {object}  settingssvc.GroupView
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/settings/{group} [get]
func (ctrl *SettingsController) ShowGroup(ctx http.Context) http.Response {
	account, role, errResp := accountCaller(ctx)
	if errResp != nil {
		return errResp
	}
	var path requests.SettingsGroupRequest
	path.Load(ctx)
	view, err := ctrl.settings.AccountGroup(ctx.Context(), account.ID, role, path.Group)
	if errResp := mapSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// Update godoc
// @Summary      Save one account settings group
// @Description  PATCH and PUT share this handler. Both routes apply policies.MayUpdateSettings (settings.write) before the handler. Owner and admin may write an account-managed group. Auditor and user may not, and that refusal does not save the group. A member who may write still gets 404 for an unknown group and 403 for a platform-managed group. One group per request. A blank or omitted secret keeps the stored value. Decimals are strings. A negative amount is not stored. Validation is HTTP 422. The account guard is not a second gate.
// @Tags         Account Settings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Param        group      path  string  true  "Settings group"
// @Success      200  {object}  settingssvc.GroupView
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/settings/{group} [patch]
// @Router       /accounts/{accountId}/settings/{group} [put]
func (ctrl *SettingsController) Update(ctx http.Context) http.Response {
	account, role, errResp := accountCaller(ctx)
	if errResp != nil {
		return errResp
	}
	var path requests.SettingsGroupRequest
	path.Load(ctx)
	group := path.Group
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}
	document, err := requests.AccountSettingsDocument(ctx)
	if err != nil {
		return mapDocumentError(ctx, err)
	}
	view, err := ctrl.settings.Save(ctx.Context(), account.ID, actorID, role, group, document)
	if errResp := mapSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// Flush godoc
// @Summary      Flush one account settings section cache
// @Description  Drops the cached rows of every account-managed group on the page. Stored values stay. An unknown section is 404. A platform-managed group is 403 and the cache is left in place.
// @Tags         Account Settings
// @Security     BearerAuth
// @Param        accountId  path  string  true  "Account UUID"
// @Param        section    path  string  true  "Settings section"
// @Success      204  "No content"
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/settings/sections/{section}/cache [post]
func (ctrl *SettingsController) Flush(ctx http.Context) http.Response {
	account, role, errResp := accountCaller(ctx)
	if errResp != nil {
		return errResp
	}
	var path requests.SettingsSectionRequest
	path.Load(ctx)
	if err := ctrl.settings.FlushSection(ctx.Context(), account.ID, role, path.Section); err != nil {
		return mapSettingsError(ctx, err)
	}
	return ctx.Response().NoContent()
}

// Reset godoc
// @Summary      Reset one account settings section
// @Description  Deletes stored rows of every account-managed group on the page. Secrets are not returned. An unknown section is 404. A platform-managed group is 403 and is left unchanged.
// @Tags         Account Settings
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Param        section    path  string  true  "Settings section"
// @Success      200  {object}  settingssvc.SectionView
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/settings/sections/{section}/reset [post]
func (ctrl *SettingsController) Reset(ctx http.Context) http.Response {
	account, role, errResp := accountCaller(ctx)
	if errResp != nil {
		return errResp
	}
	var path requests.SettingsSectionRequest
	path.Load(ctx)
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}
	view, err := ctrl.settings.ResetSection(ctx.Context(), account.ID, actorID, role, path.Section)
	if errResp := mapSettingsError(ctx, err); errResp != nil {
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

func mapDocumentError(ctx http.Context, err error) http.Response {
	if errors.Is(err, requests.ErrAccountSettingsBodyTooLarge) {
		return responses.Send(ctx, http.StatusRequestEntityTooLarge, http.Json{"error": "request body is too large"})
	}
	return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
}

func mapSettingsError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	var invalid *settingssvc.ValidationError
	if errors.As(err, &invalid) {
		return responses.Send(ctx, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{
				"code":    responses.CodeValidationFailed,
				"message": "validation failed",
			},
			"errors": invalid.Fields,
		})
	}
	switch {
	case errors.Is(err, settingssvc.ErrGroupNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "settings group not found"})
	case errors.Is(err, settingssvc.ErrSectionNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "settings section not found"})
	case errors.Is(err, settingssvc.ErrViewForbidden),
		errors.Is(err, settingssvc.ErrUpdateForbidden),
		errors.Is(err, settingssvc.ErrManagedByPlatform):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
