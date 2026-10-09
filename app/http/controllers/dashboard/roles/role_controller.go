// Package roles serves the account role catalog and the permission catalog.
// Both are the code's catalog in app/policies: there is no role or permission
// row and no write.
package roles

import (
	"github.com/goravel/framework/contracts/http"

	resources "github.com/macrowallets/waas/app/http/resources/dashboard/roles"
	"github.com/macrowallets/waas/app/policies"
)

// RoleController lists the effective grants of each account role.
type RoleController struct{}

// NewRoleController wires the role catalog handler.
func NewRoleController() *RoleController {
	return &RoleController{}
}

// Index godoc
//
//	@Summary		Account role grants
//	@Description	Effective permissions of each stored account role. Owner, admin and auditor may read. User receives 403. There is no per-account override.
//	@Tags			Account Roles
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	resources.RoleList
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/roles [get]
func (c *RoleController) Index(ctx http.Context) http.Response {
	return ctx.Response().Success().Json(resources.NewRoleList(policies.EffectiveRoleGrants()))
}
