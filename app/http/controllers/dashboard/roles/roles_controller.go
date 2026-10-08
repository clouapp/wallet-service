package roles

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
)

// Controller lists the account role catalog and the permission catalog.
type Controller struct{}

// NewController wires the dashboard role-catalog handler.
func NewController() *Controller {
	return &Controller{}
}

// Index godoc
// @Summary      Account role grants
// @Description  Effective permissions of each stored account role. Owner, admin and auditor may read. User receives 403. There is no per-account override.
// @Tags         Account Roles
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200  {object}  roleList
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/roles [get]
func (ctrl *Controller) Index(ctx http.Context) http.Response {
	account, _ := requestctx.Account(ctx)
	if account == nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
	return responses.Send(ctx, http.StatusOK, roleList{Roles: policies.EffectiveRoleGrants()})
}

// Permissions godoc
// @Summary      Account permission catalog
// @Description  Every permission the account code catalog assigns. Owner, admin and auditor may read. User receives 403. There is no permissions table and no write.
// @Tags         Account Roles
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200  {object}  permissionCatalog
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/permissions [get]
func (ctrl *Controller) Permissions(ctx http.Context) http.Response {
	account, _ := requestctx.Account(ctx)
	if account == nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
	return responses.Send(ctx, http.StatusOK, permissionCatalog{Permissions: policies.AccountPermissionCatalog()})
}

type roleList struct {
	Roles []policies.RoleGrant `json:"roles"`
}

type permissionCatalog struct {
	Permissions []string `json:"permissions"`
}
