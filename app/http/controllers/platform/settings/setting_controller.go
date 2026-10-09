package settings

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	settingsrequests "github.com/macrowallets/waas/app/http/requests/platform/settings"
	resources "github.com/macrowallets/waas/app/http/resources/platform/settings"
	"github.com/macrowallets/waas/app/http/responses"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// SettingController writes one platform settings group. S1.4.4 names
// settings.update plus the group's UpdatePermission. webhook_delivery names
// no permission, and this branch has no platform permission catalog, so a
// platform_admins row is the gate. An unknown group is 404 for a platform admin.
type SettingController struct {
	settings *settingssvc.Service
}

// NewSettingController wires the platform settings handler.
func NewSettingController(settings *settingssvc.Service) *SettingController {
	if settings == nil {
		panic("platform settings controller: settings service is required")
	}
	return &SettingController{settings: settings}
}

// Index godoc
//
//	@Summary		List platform settings
//	@Description	Sections, blocks, and platform groups. S1.4.6 names settings.view and filters by each group's ViewPermission. This branch has no platform permission catalog, so a platform_admins row is the gate and stands in for a group ViewPermission that is not in that catalog. Account groups are omitted. A secret is never returned; the field carries is_set. The read writes no activity.
//	@Tags			Platform Settings
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	settingssvc.RegistryView
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		403	{object}	responses.ErrorBody
//	@Router			/platform/settings [get]
func (c *SettingController) Index(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	view, err := c.settings.PlatformIndex(ctx.Context(), actorID)
	if err != nil {
		return mapError(ctx, err, "list platform settings")
	}

	return ctx.Response().Success().Json(view)
}

// Show godoc
//
//	@Summary		Read one platform settings group
//	@Description	One platform group. S1.4.6 names settings.view plus the group's ViewPermission. A non-admin is 403 on every platform route, whatever the group. An unknown group, including an account-only group, is 404 for a platform admin. This branch has no platform permission catalog, so a platform_admins row is the gate and stands in for a group ViewPermission that is not in that catalog. A group with no stored row is 200 with registry defaults; a secret is never returned and the field carries is_set. The read writes no activity.
//	@Tags			Platform Settings
//	@Security		BearerAuth
//	@Produce		json
//	@Param			group	path		string	true	"Settings group"
//	@Success		200		{object}	settingssvc.GroupView
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Router			/platform/settings/{group} [get]
func (c *SettingController) Show(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	group, err := requests.RouteString(ctx, "group")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "group is required")
	}

	view, err := c.settings.PlatformGroup(ctx.Context(), actorID, group)
	if err != nil {
		return mapError(ctx, err, "show platform settings group")
	}

	return ctx.Response().Success().Json(view)
}

// ShowAccount godoc
//
//	@Summary		Read one platform-managed account settings group
//	@Description	GET /v1/platform/accounts/{accountId}/settings/{group} settings.view (platform-managed account groups). settings.view is not in a platform catalog, so a platform_admins row is the gate. An unknown group, a platform-only group, and an account-managed group are 404 for a platform admin, before the account lookup. An unknown account is 404 for a platform admin. A known group with no stored row is 200 with registry defaults and is_set false. A secret is never returned. The read writes no activity and does not read another account's rows.
//	@Tags			Platform Settings
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Param			group		path		string	true	"Settings group"
//	@Success		200			{object}	settingssvc.GroupView
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/platform/accounts/{accountId}/settings/{group} [get]
func (c *SettingController) ShowAccount(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	group, err := requests.RouteString(ctx, "group")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "group is required")
	}
	// An id that is not a UUID is the nil account, which the service reports as not found.
	accountID, _ := requests.RouteUUID(ctx, "accountId")

	view, err := c.settings.PlatformAccountGroup(ctx.Context(), actorID, accountID, group)
	if err != nil {
		return mapError(ctx, err, "show platform account settings group")
	}

	return ctx.Response().Success().Json(view)
}

// UpdateAccount godoc
//
//	@Summary		Save one account sweep-limits override
//	@Description	PUT /v1/platform/accounts/{accountId}/settings/{group} settings.update + sweep.update for account_sweep_limits. Those names are not in a platform catalog, so a platform_admins row is the gate. Any other group name is 404 for a platform admin, before the account lookup. An unknown account is 404 for a platform admin. A non-admin is 403 whatever the account or group. Counts must be positive integers. Zero or negative counts, and a negative daily cap, are 422 validation_failed and are not stored. A blank daily_withdraw_cap_usd is stored empty and stays unlimited. The write is this account's row. Activity is settings.updated with the account id, the group, and the field names, never the values. The account cache key for the group is forgotten.
//	@Tags			Platform Settings
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Param			group		path		string	true	"Settings group"
//	@Success		200			{object}	settingssvc.GroupView
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Router			/platform/accounts/{accountId}/settings/{group} [put]
func (c *SettingController) UpdateAccount(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	group, err := requests.RouteString(ctx, "group")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "group is required")
	}
	// An id that is not a UUID is the nil account, which the service reports as not found.
	accountID, _ := requests.RouteUUID(ctx, "accountId")

	// The group and the account answer 404 and 403 before the service reads the body.
	view, err := c.settings.SavePlatformAccountSweepLimits(ctx.Context(), actorID, accountID, group, settingsrequests.NewUpdateDocument(ctx))
	if err != nil {
		return mapError(ctx, err, "save platform account sweep limits")
	}

	return ctx.Response().Success().Json(view)
}

// Update godoc
//
//	@Summary		Save one platform settings group
//	@Description	Writes one platform group. webhook_delivery stores max_attempts and timeout_seconds. sweep_limits stores positive address and consolidate counts; a blank daily_withdraw_cap_usd means unlimited. mail_smtp stores host, port, encryption, and username; the password is sealed and omitted from the response (is_set reports whether one is stored). A blank password keeps the stored one. mail_delivery stores driver, from_address, and from_name in the clear. An invalid address, an empty name, and driver log in production are 422 and are not stored. mail_ses, mail_mailgun, mail_resend, and mail_postmark store their provider fields; each secret is sealed and omitted, a blank secret keeps the stored one, and mail_ses key and secret must be set together. price_lookup stores provider_order. price_coingecko, price_coinmarketcap, and price_coinapi store enabled and a sealed key that is omitted from the response; a blank key keeps the stored one. An unknown provider name is 422 and is not stored. provider_alchemy stores enabled and a sealed auth_token that is omitted from the response; a blank auth_token keeps the stored one. provider_helius and provider_quicknode store enabled and a sealed api_key that is omitted from the response; a blank api_key keeps the stored one. provider_etherscan stores enabled and a sealed api_key that is omitted from the response; a blank api_key keeps the stored one. Zero or negative counts, and a negative cap, are 422 and are not stored. An unknown group is 404 for a platform admin. Values are not written to the activity log.
//	@Tags			Platform Settings
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			group	path		string	true	"Settings group"
//	@Success		200		{object}	settingssvc.GroupView
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Router			/platform/settings/{group} [put]
func (c *SettingController) Update(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	group, err := requests.RouteString(ctx, "group")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "group is required")
	}
	var req settingsrequests.UpdateRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "save platform settings group")
	}

	view, err := c.settings.SavePlatform(ctx.Context(), actorID, group, req.Document)
	if err != nil {
		return mapError(ctx, err, "save platform settings group")
	}

	return ctx.Response().Success().Json(view)
}

// Flush godoc
//
//	@Summary		Flush one platform settings section cache
//	@Description	Drops the settings:platform cache key of every platform group on the page. Stored rows stay. No activity row. An unknown section, including an account-only page, is 404 for a platform admin. S1.4.6 names settings.update for every group on the page. This branch has no platform permission catalog, so a platform_admins row is the gate.
//	@Tags			Platform Settings
//	@Security		BearerAuth
//	@Param			section	path	string	true	"Settings section"
//	@Success		204		"No content"
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Router			/platform/settings/sections/{section}/cache [post]
func (c *SettingController) Flush(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	section, _ := requests.RouteString(ctx, "section")

	if err := c.settings.FlushPlatformSection(ctx.Context(), actorID, section); err != nil {
		return mapError(ctx, err, "flush platform settings section")
	}

	return ctx.Response().NoContent()
}

// Reset godoc
//
//	@Summary		Reset one platform settings section
//	@Description	Deletes the stored rows of every platform group on the page and drops their settings:platform cache keys. The next read uses registry defaults. Account rows and account cache keys stay. Activity is settings.section_reset with a null account id and names each group and its field names, never the values. An unknown section, including an account-only page, is 404 for a platform admin. S1.4.6 names settings.update. This branch has no platform permission catalog, so a platform_admins row is the gate.
//	@Tags			Platform Settings
//	@Security		BearerAuth
//	@Produce		json
//	@Param			section	path		string	true	"Settings section"
//	@Success		200		{object}	settingssvc.SectionView
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Router			/platform/settings/sections/{section}/reset [post]
func (c *SettingController) Reset(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	section, _ := requests.RouteString(ctx, "section")

	view, err := c.settings.ResetPlatformSection(ctx.Context(), actorID, section)
	if err != nil {
		return mapError(ctx, err, "reset platform settings section")
	}

	return ctx.Response().Success().Json(view)
}

// Test godoc
//
//	@Summary		Send one platform mail test
//	@Description	POST /v1/platform/settings/mail/test. S1.4.6: settings.update + mail.update, declared before {group}. Neither permission is in the platform catalog, so a platform_admins row is the gate. A non-admin is 403 before the body is read. The body field is to. An invalid address is 422 validation_failed and nothing is sent. One message goes through facades.Mail, which reads mail_smtp and mail_delivery at send time. The answer is {"sent": true}. A mailer failure is 502 {"error":{"code","message"}} and the message does not include the password or the SMTP host credentials. The test is not audited.
//	@Tags			Platform Settings
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Success		200	{object}	resources.MailTestSent
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		403	{object}	responses.ErrorBody
//	@Failure		422	{object}	responses.ErrorBody
//	@Failure		502	{object}	responses.ErrorBody
//	@Router			/platform/settings/mail/test [post]
func (c *SettingController) Test(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	// A non-admin answers 403 before the service reads the body.
	if err := c.settings.SendPlatformMailTest(ctx.Context(), actorID, settingsrequests.NewMailTestRecipient(ctx)); err != nil {
		return mapError(ctx, err, "send platform mail test")
	}

	return ctx.Response().Success().Json(resources.NewMailTestSent())
}
