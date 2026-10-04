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
// @Description  Writes one platform group. webhook_delivery stores max_attempts and timeout_seconds. sweep_limits stores positive address and consolidate counts; a blank daily_withdraw_cap_usd means unlimited. mail_smtp stores host, port, encryption, and username; the password is sealed and omitted from the response (is_set reports whether one is stored). A blank password keeps the stored one. mail_delivery stores driver, from_address, and from_name in the clear. An invalid address, an empty name, and driver log in production are 422 and are not stored. mail_ses, mail_mailgun, mail_resend, and mail_postmark store their provider fields; each secret is sealed and omitted, a blank secret keeps the stored one, and mail_ses key and secret must be set together. price_lookup stores provider_order. price_coingecko, price_coinmarketcap, and price_coinapi store enabled and a sealed key that is omitted from the response; a blank key keeps the stored one. An unknown provider name is 422 and is not stored. provider_alchemy stores enabled and a sealed auth_token that is omitted from the response; a blank auth_token keeps the stored one. provider_helius and provider_quicknode store enabled and a sealed api_key that is omitted from the response; a blank api_key keeps the stored one. Zero or negative counts, and a negative cap, are 422 and are not stored. An unknown group is 404 before the platform-admin check. Values are not written to the activity log.
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
