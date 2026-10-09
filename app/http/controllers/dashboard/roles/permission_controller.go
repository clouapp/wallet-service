package roles

import (
	"github.com/goravel/framework/contracts/http"

	resources "github.com/macrowallets/waas/app/http/resources/dashboard/roles"
	"github.com/macrowallets/waas/app/policies"
)

// PermissionController lists the account permission catalog.
type PermissionController struct{}

// NewPermissionController wires the permission catalog handler.
func NewPermissionController() *PermissionController {
	return &PermissionController{}
}

// Index godoc
//
//	@Summary		Account permission catalog
//	@Description	Every permission the account code catalog assigns. Owner, admin and auditor may read. User receives 403. There is no permissions table and no write.
//	@Tags			Account Roles
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	resources.PermissionCatalog
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/permissions [get]
func (c *PermissionController) Index(ctx http.Context) http.Response {
	return ctx.Response().Success().Json(resources.NewPermissionCatalog(policies.AccountPermissionCatalog()))
}
