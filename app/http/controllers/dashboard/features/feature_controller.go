// Package features lists an account's feature flags on the dashboard. The
// flags are platform-controlled; there is no account-side write.
package features

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	featuressvc "github.com/macrowallets/waas/app/services/features"
)

// FeatureController lists the feature flags of the account in scope.
type FeatureController struct {
	features *featuressvc.Service
}

// NewFeatureController wires the feature flag handler to the features service.
func NewFeatureController(features *featuressvc.Service) *FeatureController {
	if features == nil {
		panic("dashboard account features controller: features service is required")
	}
	return &FeatureController{features: features}
}

// Index godoc
//
//	@Summary		Account feature flags
//	@Description	GET /v1/accounts/{accountId}/features applies policies.MayViewSettings (settings.read) before the handler. Owner, admin, and auditor may list. A user may not, and that refusal does not return the feature list. The refusal is 403 with the message the service returns. A missing row uses the catalog default. There is no account-side write.
//	@Tags			Account Features
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	featuressvc.List
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/features [get]
func (c *FeatureController) Index(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	role, _ := requestctx.AccountRole(ctx)

	view, err := c.features.List(ctx.Context(), account.ID, role)
	if err != nil {
		return mapError(ctx, err, "internal_error")
	}

	return ctx.Response().Success().Json(view)
}
