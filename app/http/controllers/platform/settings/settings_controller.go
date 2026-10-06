package settings

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/mails"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// mailTestFailedMessage is the 502 text. It names neither the SMTP password
// nor the host credentials. The provider error stays off the wire and the log.
const mailTestFailedMessage = "the test message was not sent"

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

// Index godoc
// @Summary      List platform settings
// @Description  Sections, blocks, and platform groups. S1.4.6 names settings.view and filters by each group's ViewPermission. This branch has no platform permission catalog, so a platform_admins row is the gate and stands in for a group ViewPermission that is not in that catalog. Account groups are omitted. A secret is never returned; the field carries is_set. The read writes no activity.
// @Tags         Platform Settings
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  settingssvc.RegistryView
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Router       /platform/settings [get]
func (ctrl *SettingsController) Index(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	view, err := ctrl.settings.PlatformIndex(ctx.Context(), actorID)
	if errResp := mapPlatformSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// Show godoc
// @Summary      Read one platform settings group
// @Description  One platform group. S1.4.6 names settings.view plus the group's ViewPermission, and 404 before 403. An unknown group, including an account-only group, is 404 before the platform-admin check. This branch has no platform permission catalog, so a platform_admins row is the gate and stands in for a group ViewPermission that is not in that catalog. A group with no stored row is 200 with registry defaults; a secret is never returned and the field carries is_set. The read writes no activity.
// @Tags         Platform Settings
// @Security     BearerAuth
// @Produce      json
// @Param        group  path  string  true  "Settings group"
// @Success      200  {object}  settingssvc.GroupView
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/settings/{group} [get]
func (ctrl *SettingsController) Show(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	var path requests.SettingsGroupRequest
	path.Load(ctx)
	if path.Group == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "group is required"})
	}
	view, err := ctrl.settings.PlatformGroup(ctx.Context(), actorID, path.Group)
	if errResp := mapPlatformSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// ShowAccount godoc
// @Summary      Read one platform-managed account settings group
// @Description  GET /v1/platform/accounts/{accountId}/settings/{group} settings.view (platform-managed account groups). settings.view is not in a platform catalog, so a platform_admins row is the gate. An unknown group, a platform-only group, and an account-managed group are 404 before the account lookup and before that gate. An unknown account is 404 before that gate. A known group with no stored row is 200 with registry defaults and is_set false. A secret is never returned. The read writes no activity and does not read another account's rows.
// @Tags         Platform Settings
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Param        group      path  string  true  "Settings group"
// @Success      200  {object}  settingssvc.GroupView
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/accounts/{accountId}/settings/{group} [get]
func (ctrl *SettingsController) ShowAccount(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	var path requests.PlatformAccountSettingsRequest
	path.Load(ctx)
	if path.Group == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "group is required"})
	}
	accountID, err := uuid.Parse(path.AccountID)
	if err != nil {
		accountID = uuid.Nil
	}
	view, err := ctrl.settings.PlatformAccountGroup(ctx.Context(), actorID, accountID, path.Group)
	if errResp := mapPlatformSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// UpdateAccount godoc
// @Summary      Save one account sweep-limits override
// @Description  PUT /v1/platform/accounts/{accountId}/settings/{group} settings.update + sweep.update for account_sweep_limits. Those names are not in a platform catalog, so a platform_admins row is the gate. Any other group name is 404 before the account lookup and before that gate. An unknown account is 404 before that gate. A non-admin on a known account and account_sweep_limits is 403. Counts must be positive integers. Zero or negative counts, and a negative daily cap, are 422 validation_failed and are not stored. A blank daily_withdraw_cap_usd is stored empty and stays unlimited. The write is this account's row. Activity is settings.updated with the account id, the group, and the field names, never the values. The account cache key for the group is forgotten.
// @Tags         Platform Settings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Param        group      path  string  true  "Settings group"
// @Success      200  {object}  settingssvc.GroupView
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /platform/accounts/{accountId}/settings/{group} [put]
func (ctrl *SettingsController) UpdateAccount(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	var path requests.PlatformAccountSettingsRequest
	path.Load(ctx)
	if path.Group == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "group is required"})
	}
	accountID, err := uuid.Parse(path.AccountID)
	if err != nil {
		accountID = uuid.Nil
	}
	if err := ctrl.settings.AuthorizePlatformAccountSweepWrite(ctx.Context(), actorID, accountID, path.Group); err != nil {
		return mapPlatformSettingsError(ctx, err)
	}
	document, err := requests.AccountSettingsDocument(ctx)
	if err != nil {
		return mapPlatformSettingsBodyError(ctx, err)
	}
	view, err := ctrl.settings.SavePlatformAccountSweepLimits(ctx.Context(), actorID, accountID, path.Group, document)
	if errResp := mapPlatformSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// Update godoc
// @Summary      Save one platform settings group
// @Description  Writes one platform group. webhook_delivery stores max_attempts and timeout_seconds. sweep_limits stores positive address and consolidate counts; a blank daily_withdraw_cap_usd means unlimited. mail_smtp stores host, port, encryption, and username; the password is sealed and omitted from the response (is_set reports whether one is stored). A blank password keeps the stored one. mail_delivery stores driver, from_address, and from_name in the clear. An invalid address, an empty name, and driver log in production are 422 and are not stored. mail_ses, mail_mailgun, mail_resend, and mail_postmark store their provider fields; each secret is sealed and omitted, a blank secret keeps the stored one, and mail_ses key and secret must be set together. price_lookup stores provider_order. price_coingecko, price_coinmarketcap, and price_coinapi store enabled and a sealed key that is omitted from the response; a blank key keeps the stored one. An unknown provider name is 422 and is not stored. provider_alchemy stores enabled and a sealed auth_token that is omitted from the response; a blank auth_token keeps the stored one. provider_helius and provider_quicknode store enabled and a sealed api_key that is omitted from the response; a blank api_key keeps the stored one. provider_etherscan stores enabled and a sealed api_key that is omitted from the response; a blank api_key keeps the stored one. Zero or negative counts, and a negative cap, are 422 and are not stored. An unknown group is 404 before the platform-admin check. Values are not written to the activity log.
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

// Flush godoc
// @Summary      Flush one platform settings section cache
// @Description  Drops the settings:platform cache key of every platform group on the page. Stored rows stay. No activity row. An unknown section, including an account-only page, is 404 before the platform-admin check. S1.4.6 names settings.update for every group on the page. This branch has no platform permission catalog, so a platform_admins row is the gate.
// @Tags         Platform Settings
// @Security     BearerAuth
// @Param        section  path  string  true  "Settings section"
// @Success      204  "No content"
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/settings/sections/{section}/cache [post]
func (ctrl *SettingsController) Flush(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	var path requests.SettingsSectionRequest
	path.Load(ctx)
	if err := ctrl.settings.FlushPlatformSection(ctx.Context(), actorID, path.Section); err != nil {
		return mapPlatformSettingsError(ctx, err)
	}
	return ctx.Response().NoContent()
}

// Reset godoc
// @Summary      Reset one platform settings section
// @Description  Deletes the stored rows of every platform group on the page and drops their settings:platform cache keys. The next read uses registry defaults. Account rows and account cache keys stay. Activity is settings.section_reset with a null account id and names each group and its field names, never the values. An unknown section, including an account-only page, is 404 before the platform-admin check. S1.4.6 names settings.update. This branch has no platform permission catalog, so a platform_admins row is the gate.
// @Tags         Platform Settings
// @Security     BearerAuth
// @Produce      json
// @Param        section  path  string  true  "Settings section"
// @Success      200  {object}  settingssvc.SectionView
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/settings/sections/{section}/reset [post]
func (ctrl *SettingsController) Reset(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	var path requests.SettingsSectionRequest
	path.Load(ctx)
	view, err := ctrl.settings.ResetPlatformSection(ctx.Context(), actorID, path.Section)
	if errResp := mapPlatformSettingsError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// TestMail godoc
// @Summary      Send one platform mail test
// @Description  POST /v1/platform/settings/mail/test. S1.4.6: settings.update + mail.update, declared before {group}. Neither permission is in the platform catalog, so a platform_admins row is the gate. A non-admin is 403 before the body is read. The body field is to. An invalid address is 422 validation_failed and nothing is sent. One message goes through facades.Mail, which reads mail_smtp and mail_delivery at send time. The answer is {"sent": true}. A mailer failure is 502 {"error":{"code","message"}} and the message does not include the password or the SMTP host credentials. The test is not audited.
// @Tags         Platform Settings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]bool
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Failure      502  {object}  responses.ErrorBody
// @Router       /platform/settings/mail/test [post]
func (ctrl *SettingsController) TestMail(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	if err := ctrl.settings.AuthorizePlatformMailTest(ctx.Context(), actorID); err != nil {
		return mapPlatformSettingsError(ctx, err)
	}
	var req requests.PlatformMailTestRequest
	if resp := requests.Validate(ctx, &req); resp != nil {
		return resp
	}
	err := appfacades.Mail().To([]string{req.To}).Send(&mails.SettingsTestMail{To: req.To})
	if err != nil {
		appfacades.Log().Error(mailTestFailedMessage)
		return responses.Send(ctx, http.StatusBadGateway, http.Json{"error": mailTestFailedMessage})
	}
	return responses.Send(ctx, http.StatusOK, http.Json{"sent": true})
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
	case errors.Is(err, settingssvc.ErrAccountNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, settingssvc.ErrAccountNotFound.Error())
	case errors.Is(err, settingssvc.ErrSectionNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "settings section not found"})
	case errors.Is(err, settingssvc.ErrPlatformForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrPlatformForbidden.Error())
	case errors.Is(err, settingssvc.ErrPlatformViewForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrPlatformViewForbidden.Error())
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
