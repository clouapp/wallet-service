package settings

import (
	"errors"
	"strings"

	"github.com/goravel/framework/contracts/http"

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
	return ctx.Response().Json(http.StatusOK, view)
}

// Update godoc
// @Summary      Save one account settings group
// @Description  One group per request. A blank or omitted secret keeps the stored value. Decimals are strings. An unknown group is 404.
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
func (ctrl *SettingsController) Update(ctx http.Context) http.Response {
	account, role, errResp := accountCaller(ctx)
	if errResp != nil {
		return errResp
	}
	group := strings.TrimSpace(ctx.Request().Route("group"))
	document, err := requests.AccountSettingsDocument(ctx)
	if err != nil {
		return mapDocumentError(ctx, err)
	}
	view, err := ctrl.settings.Save(ctx.Context(), account.ID, role, group, document)
	if errResp := mapSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Json(http.StatusOK, view)
}

func accountCaller(ctx http.Context) (*models.Account, string, http.Response) {
	account, _ := ctx.Value("account").(*models.Account)
	if account == nil {
		return nil, "", responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
	role, _ := ctx.Value("account_role").(string)
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
		return ctx.Response().Json(http.StatusUnprocessableEntity, map[string]any{
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
	case errors.Is(err, settingssvc.ErrViewForbidden),
		errors.Is(err, settingssvc.ErrUpdateForbidden),
		errors.Is(err, settingssvc.ErrManagedByPlatform):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
