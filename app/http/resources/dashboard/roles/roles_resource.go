package roles

import (
	"github.com/macrowallets/waas/app/policies"
)

// RoleList is GET /v1/accounts/{accountId}/roles: the effective grants of each
// stored account role.
type RoleList struct {
	Roles []policies.RoleGrant `json:"roles"`
}

// NewRoleList shapes the role grants.
func NewRoleList(grants []policies.RoleGrant) RoleList {
	return RoleList{Roles: grants}
}

// PermissionCatalog is GET /v1/accounts/{accountId}/permissions: every
// permission the account catalog assigns.
type PermissionCatalog struct {
	Permissions []string `json:"permissions"`
}

// NewPermissionCatalog shapes the permission catalog.
func NewPermissionCatalog(permissions []string) PermissionCatalog {
	return PermissionCatalog{Permissions: permissions}
}
