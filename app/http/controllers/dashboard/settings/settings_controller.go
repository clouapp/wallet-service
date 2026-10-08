package settings

import (
	"errors"
	"strings"

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
	return ctx.Response().Success().Json(view)
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
	view, err := ctrl.settings.AccountGroup(ctx.Context(), account.ID, role, strings.TrimSpace(ctx.Request().Route("group")))
	if errResp := mapSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Success().Json(view)
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
	group := strings.TrimSpace(ctx.Request().Route("group"))
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
	}
	document, err := requests.AccountSettingsDocument(ctx)
	if err != nil {
		return mapDocumentError(ctx, err)
	}
	view, err := ctrl.settings.Save(ctx.Context(), account.ID, actorID, role, group, document)
	if errResp := mapSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Success().Json(view)
}

// Flush godoc
// @Summary      Flush one account settings section cache
// @Description  POST /v1/accounts/{accountId}/settings/sections/{section}/cache applies policies.MayUpdateSettings (settings.write) before the handler. Owner and admin may flush an account-managed section. Auditor and user may not, and that refusal does not flush the section. The refusal is 403 with the same message this handler returns. Drops the cached rows of every account-managed group on the page. Stored values stay. An unknown section is 404 for a role that may flush. A platform-managed group is 403 and the cache is left in place.
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
	if err := ctrl.settings.FlushSection(ctx.Context(), account.ID, role, strings.TrimSpace(ctx.Request().Route("section"))); err != nil {
		return mapSettingsError(ctx, err)
	}
	return ctx.Response().NoContent()
}

// Reset godoc
// @Summary      Reset one account settings section
// @Description  POST /v1/accounts/{accountId}/settings/sections/{section}/reset applies policies.MayUpdateSettings (settings.write) before the handler. Owner and admin may reset an account-managed section. Auditor and user may not, and that refusal does not reset the section. The refusal is 403 with the same message the flush route returns. Deletes stored rows of every account-managed group on the page. Secrets are not returned. An unknown section is 404 for a role that may reset. A platform-managed group is 403 and is left unchanged.
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
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
	}
	view, err := ctrl.settings.ResetSection(ctx.Context(), account.ID, actorID, role, strings.TrimSpace(ctx.Request().Route("section")))
	if errResp := mapSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Success().Json(view)
}

func accountCaller(ctx http.Context) (*models.Account, string, http.Response) {
	account, _ := requestctx.Account(ctx)
	if account == nil {
		return nil, "", responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
	role, _ := requestctx.AccountRole(ctx)
	return account, role, nil
}

func mapDocumentError(ctx http.Context, err error) http.Response {
	if errors.Is(err, requests.ErrAccountSettingsBodyTooLarge) {
		return responses.Fail(ctx, http.StatusRequestEntityTooLarge, responses.CodeRequestTooLarge, "request body is too large")
	}
	return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
}

func mapSettingsError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	var invalid *settingssvc.ValidationError
	if errors.As(err, &invalid) {
		return responses.FieldsFailed(ctx, invalid.Fields)
	}
	switch {
	case errors.Is(err, settingssvc.ErrGroupNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "settings group not found")
	case errors.Is(err, settingssvc.ErrSectionNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "settings section not found")
	case errors.Is(err, settingssvc.ErrViewForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrViewForbidden.Error())
	case errors.Is(err, settingssvc.ErrUpdateForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrUpdateForbidden.Error())
	case errors.Is(err, settingssvc.ErrManagedByPlatform):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrManagedByPlatform.Error())
	default:
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
}
