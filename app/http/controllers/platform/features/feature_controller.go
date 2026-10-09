package features

import (
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	featuresrequests "github.com/macrowallets/waas/app/http/requests/platform/features"
	featuressvc "github.com/macrowallets/waas/app/services/features"
)

// FeatureController serves platform feature flags. Only a platform admin may
// call it. An account owner who is not in platform_admins receives 403.
type FeatureController struct {
	features *featuressvc.Service
	accounts featuressvc.Accounts
}

// NewFeatureController wires the platform feature-flag handlers.
func NewFeatureController(features *featuressvc.Service, accounts featuressvc.Accounts) *FeatureController {
	if features == nil {
		panic("platform features controller: features service is required")
	}
	if accounts == nil {
		panic("platform features controller: accounts are required")
	}
	return &FeatureController{features: features, accounts: accounts}
}

// Index godoc
//
//	@Summary		Platform feature flags
//	@Description	Every named flag for the platform. A missing row uses the catalog default (withdrawals and sweep on). Only a platform admin may read.
//	@Tags			Platform Features
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	featuressvc.List
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		403	{object}	responses.ErrorBody
//	@Router			/platform/features [get]
func (c *FeatureController) Index(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	view, err := c.features.ListGlobal(ctx.Context(), actorID)
	if err != nil {
		return mapError(ctx, err, "list platform features")
	}

	return ctx.Response().Success().Json(view)
}

// Update godoc
//
//	@Summary		Set one platform feature flag
//	@Description	Writes one boolean and returns the stored row. Only a platform admin may write. An unknown key is 404. The next gate reads the row; nothing is cached.
//	@Tags			Platform Features
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			key	path		string							true	"Feature key"
//	@Success		200	{object}	featuressvc.Flag
//	@Failure		403	{object}	responses.ErrorBody
//	@Failure		404	{object}	responses.ErrorBody
//	@Failure		422	{object}	responses.ErrorBody
//	@Router			/platform/features/{key} [patch]
func (c *FeatureController) Update(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	key := strings.TrimSpace(ctx.Request().Route("key"))
	var req featuresrequests.UpdateRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "set platform feature")
	}

	flag, err := c.features.SetGlobal(ctx.Context(), actorID, key, req.Enabled)
	if err != nil {
		return mapError(ctx, err, "set platform feature")
	}

	return ctx.Response().Success().Json(flag)
}

// ShowScope godoc
//
//	@Summary		One account's feature flags
//	@Description	S2.4 GET /v1/platform/features/{scope}/{id}. features.view is not a permission row, so a platform_admins row is the gate. Scope account returns that account's stored booleans. A missing row is the catalog default and is not inserted. Global rows are not applied. global, user, and chain are 404 for a platform admin. An unknown account is 404 for a platform admin.
//	@Tags			Platform Features
//	@Security		BearerAuth
//	@Produce		json
//	@Param			scope	path		string	true	"Feature scope"
//	@Param			id		path		string	true	"Account UUID"
//	@Success		200		{object}	featuressvc.List
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Router			/platform/features/{scope}/{id} [get]
func (c *FeatureController) ShowScope(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	scope := strings.TrimSpace(ctx.Request().Route("scope"))
	id := strings.TrimSpace(ctx.Request().Route("id"))

	view, err := c.features.ListScopedForPlatform(ctx.Context(), actorID, scope, id, c.accounts)
	if err != nil {
		return mapError(ctx, err, "show platform account features")
	}

	return ctx.Response().Success().Json(view)
}

// UpdateScope godoc
//
//	@Summary		Set account feature flags
//	@Description	S2.4 PUT /v1/platform/features/{scope}/{id}. FeaturePolicy names features.update for any scope and features.account.update for the account scope. Neither is a permission row, so a platform_admins row is the gate and stands in for both. The pair is not a second gate. The body is {"features":[{"key","enabled"}]}. Every key is checked before the first write. An unknown key stores nothing. global, user, and chain are 404 for a platform admin. An unknown account is 404 for a platform admin. A closed global row is not applied and is not written. Omitted flags are not inserted.
//	@Tags			Platform Features
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			scope	path		string	true	"Feature scope"
//	@Param			id		path		string	true	"Account UUID"
//	@Success		200		{object}	featuressvc.List
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Router			/platform/features/{scope}/{id} [put]
func (c *FeatureController) UpdateScope(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	var scope featuresrequests.ScopeRequest
	if err := scope.Decode(ctx); err != nil {
		return mapError(ctx, err, "set platform account features")
	}
	var req featuresrequests.UpdateScopeRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "set platform account features")
	}

	view, err := c.features.SetScopedForPlatform(ctx.Context(), actorID, scope.Scope, scope.ID, req.Writes(), c.accounts)
	if err != nil {
		return mapError(ctx, err, "set platform account features")
	}

	return ctx.Response().Success().Json(view)
}

// UpdateScopeFeature godoc
//
//	@Summary		Set one account feature flag
//	@Description	S2.4 PUT /v1/platform/features/{scope}/{id}/{feature}. FeaturePolicy names features.update for any scope and features.account.update for the account scope. Neither is a permission row, so a platform_admins row is the gate and stands in for both. The pair is not a second gate. The body is {"enabled":bool}. An unknown key stores nothing. global, user, and chain are 404 for a platform admin. An unknown account is 404 for a platform admin. A closed global row is not applied and is not written.
//	@Tags			Platform Features
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			scope	path		string	true	"Feature scope"
//	@Param			id		path		string	true	"Account UUID"
//	@Param			feature	path		string	true	"Feature key"
//	@Success		200		{object}	featuressvc.Flag
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Router			/platform/features/{scope}/{id}/{feature} [put]
func (c *FeatureController) UpdateScopeFeature(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	var scope featuresrequests.ScopeRequest
	if err := scope.Decode(ctx); err != nil {
		return mapError(ctx, err, "set platform account feature")
	}
	feature := strings.TrimSpace(ctx.Request().Route("feature"))
	var req featuresrequests.UpdateRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "set platform account feature")
	}

	flag, err := c.features.SetScopedFlagForPlatform(ctx.Context(), actorID, scope.Scope, scope.ID, feature, req.Enabled, c.accounts)
	if err != nil {
		return mapError(ctx, err, "set platform account feature")
	}

	return ctx.Response().Success().Json(flag)
}
