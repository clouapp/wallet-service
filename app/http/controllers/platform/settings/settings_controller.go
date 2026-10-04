package settings

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// SettingsController writes one platform settings group. S1.4.4 names
// settings.update plus the group's UpdatePermission. webhook_delivery names
// no permission, and this branch has no platform permission catalog, so a
// platform_admins row is the gate. An unknown group is 404 before that check.
type SettingsController struct {
	settings *settingssvc.Service
}

// NewSettingsController wires the platform settings handler.
func NewSettingsController(settings *settingssvc.Service) *SettingsController {
	if settings == nil {
		panic("platform settings controller: settings service is required")
	}
	return &SettingsController{settings: settings}
}

// Update godoc
// @Summary      Save one platform settings group
// @Description  Writes webhook_delivery max_attempts and timeout_seconds. Zero or negative is 422 and is not stored. An unknown group is 404 before the platform-admin check. Values are not written to the activity log.
// @Tags         Platform Settings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        group  path  string  true  "Settings group"
// @Success      200  {object}  settingssvc.GroupView
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /platform/settings/{group} [put]
func (ctrl *SettingsController) Update(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	var path requests.SettingsGroupRequest
	path.Load(ctx)
	if path.Group == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "group is required"})
	}
	document, err := requests.AccountSettingsDocument(ctx)
	if err != nil {
		return mapPlatformSettingsBodyError(ctx, err)
	}
	view, err := ctrl.settings.SavePlatform(ctx.Context(), actorID, path.Group, document)
	if errResp := mapPlatformSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

func mapPlatformSettingsBodyError(ctx http.Context, err error) http.Response {
	if errors.Is(err, requests.ErrAccountSettingsBodyTooLarge) {
		return responses.Send(ctx, http.StatusRequestEntityTooLarge, http.Json{"error": "request body is too large"})
	}
	return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
}

func mapPlatformSettingsError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	var invalid *settingssvc.ValidationError
	if errors.As(err, &invalid) {
		return responses.FieldsFailed(ctx, invalid.Fields)
	}
	switch {
	case errors.Is(err, settingssvc.ErrGroupNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "settings group not found"})
	case errors.Is(err, settingssvc.ErrPlatformForbidden):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
