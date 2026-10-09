// Package settings serves the settings registry of the account in scope on
// the dashboard. settings.read and settings.write are route middleware; the
// group and section rules stay in the settings service.
package settings

import (
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// SettingsController reads, saves, flushes and resets the account's settings.
type SettingsController struct {
	settings *settingssvc.Service
}

// NewSettingsController wires the account settings handlers to the settings service.
func NewSettingsController(settings *settingssvc.Service) *SettingsController {
	if settings == nil {
		panic("dashboard account settings controller: settings service is required")
	}
	return &SettingsController{settings: settings}
}

// Index godoc
//
//	@Summary		Account settings registry
//	@Description	Sections, blocks and groups for one account. A secret is never returned; the field carries is_set.
//	@Tags			Account Settings
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	settingssvc.RegistryView
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/settings [get]
func (c *SettingsController) Index(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	role, _ := requestctx.AccountRole(ctx)

	view, err := c.settings.Registry(ctx.Context(), account.ID, role)
	if err != nil {
		return mapError(ctx, err, "internal_error")
	}

	return ctx.Response().Success().Json(view)
}

// Show godoc
//
//	@Summary		Read one account settings group
//	@Description	GET /v1/accounts/{accountId}/settings/{group} settings.read. The route applies policies.MayViewSettings before the handler. Owner, admin, and auditor may read, including a platform-managed group. A user may not, and that refusal does not return the group. A member who may read still gets 404 for an unknown group and a platform-only group. The account guard is not a second gate. A secret is never returned. The read writes no activity and does not return another account's rows.
//	@Tags			Account Settings
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Param			group		path		string	true	"Settings group"
//	@Success		200			{object}	settingssvc.GroupView
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/settings/{group} [get]
func (c *SettingsController) Show(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	role, _ := requestctx.AccountRole(ctx)

	group := strings.TrimSpace(ctx.Request().Route("group"))

	view, err := c.settings.AccountGroup(ctx.Context(), account.ID, role, group)
	if err != nil {
		return mapError(ctx, err, "internal_error")
	}

	return ctx.Response().Success().Json(view)
}

// Update godoc
//
//	@Summary		Save one account settings group
//	@Description	PATCH and PUT share this handler. Both routes apply policies.MayUpdateSettings (settings.write) before the handler. Owner and admin may write an account-managed group. Auditor and user may not, and that refusal does not save the group. A member who may write still gets 404 for an unknown group and 403 for a platform-managed group. One group per request. A blank or omitted secret keeps the stored value. Decimals are strings. A negative amount is not stored. Validation is HTTP 422. The account guard is not a second gate.
//	@Tags			Account Settings
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Param			group		path		string	true	"Settings group"
//	@Success		200			{object}	settingssvc.GroupView
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/settings/{group} [patch]
//	@Router			/accounts/{accountId}/settings/{group} [put]
func (c *SettingsController) Update(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	role, _ := requestctx.AccountRole(ctx)
	actorID := requestctx.MustUserID(ctx)

	group := strings.TrimSpace(ctx.Request().Route("group"))
	document, err := requests.AccountSettingsDocument(ctx)
	if err != nil {
		return mapDocumentError(ctx, err)
	}

	view, err := c.settings.Save(ctx.Context(), account.ID, actorID, role, group, document)
	if err != nil {
		return mapError(ctx, err, "internal_error")
	}

	return ctx.Response().Success().Json(view)
}

// Flush godoc
//
//	@Summary		Flush one account settings section cache
//	@Description	POST /v1/accounts/{accountId}/settings/sections/{section}/cache applies policies.MayUpdateSettings (settings.write) before the handler. Owner and admin may flush an account-managed section. Auditor and user may not, and that refusal does not flush the section. The refusal is 403 with the same message this handler returns. Drops the cached rows of every account-managed group on the page. Stored values stay. An unknown section is 404 for a role that may flush. A platform-managed group is 403 and the cache is left in place.
//	@Tags			Account Settings
//	@Security		BearerAuth
//	@Param			accountId	path	string	true	"Account UUID"
//	@Param			section		path	string	true	"Settings section"
//	@Success		204			"No content"
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/settings/sections/{section}/cache [post]
func (c *SettingsController) Flush(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	role, _ := requestctx.AccountRole(ctx)

	section := strings.TrimSpace(ctx.Request().Route("section"))

	if err := c.settings.FlushSection(ctx.Context(), account.ID, role, section); err != nil {
		return mapError(ctx, err, "internal_error")
	}

	return ctx.Response().NoContent()
}

// Reset godoc
//
//	@Summary		Reset one account settings section
//	@Description	POST /v1/accounts/{accountId}/settings/sections/{section}/reset applies policies.MayUpdateSettings (settings.write) before the handler. Owner and admin may reset an account-managed section. Auditor and user may not, and that refusal does not reset the section. The refusal is 403 with the same message the flush route returns. Deletes stored rows of every account-managed group on the page. Secrets are not returned. An unknown section is 404 for a role that may reset. A platform-managed group is 403 and is left unchanged.
//	@Tags			Account Settings
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Param			section		path		string	true	"Settings section"
//	@Success		200			{object}	settingssvc.SectionView
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/settings/sections/{section}/reset [post]
func (c *SettingsController) Reset(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	role, _ := requestctx.AccountRole(ctx)
	actorID := requestctx.MustUserID(ctx)

	section := strings.TrimSpace(ctx.Request().Route("section"))

	view, err := c.settings.ResetSection(ctx.Context(), account.ID, actorID, role, section)
	if err != nil {
		return mapError(ctx, err, "internal_error")
	}

	return ctx.Response().Success().Json(view)
}
