package policies

import (
	"sort"

	"github.com/macrowallets/waas/app/models"
)

// RoleGrant is one stored account role and the permissions that role holds.
// The list is the code catalog the live gates enforce. There is no
// account_role_permissions row, so nothing here is an override.
type RoleGrant struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

// EffectiveRoleGrants lists every stored account role and the permissions
// that role holds. Owner and admin may withdraw, sweep and create a wallet.
// User may generate an address and may not withdraw, sweep or create a
// wallet. Auditor is read-only. roles.write is absent.
func EffectiveRoleGrants() []RoleGrant {
	names := models.AccountRoles()
	grants := make([]RoleGrant, 0, len(names))
	for _, role := range names {
		grants = append(grants, RoleGrant{
			Role:        role,
			Permissions: rolePermissions(role),
		})
	}
	return grants
}

func rolePermissions(role string) []string {
	held := map[string]struct{}{}
	for permission := range AccountRoleGrants(role) {
		held[permission] = struct{}{}
	}
	if MayReadActivity(role) {
		held[PermActivityRead] = struct{}{}
	}
	if MayReadTokens(role) {
		held[PermTokensRead] = struct{}{}
	}
	if MayWriteTokens(role) {
		held[PermTokensWrite] = struct{}{}
	}
	for permission := range WalletGrants(role) {
		held[permission] = struct{}{}
	}
	if MayPerformFundAction(role, FundWithdraw) {
		held[PermWithdrawalsCreate] = struct{}{}
	}
	if MayPerformFundAction(role, FundSweep) {
		held[PermSweepExecute] = struct{}{}
	}
	if MayPerformFundAction(role, FundCreateWallet) {
		held[PermWalletsCreate] = struct{}{}
	}
	if SeesEveryAccountWallet(role, false) {
		held[PermWalletsRead] = struct{}{}
	}
	if role == roleOwner || role == roleAdmin {
		held[PermWebhooksWrite] = struct{}{}
	}
	names := make([]string, 0, len(held))
	for permission := range held {
		names = append(names, permission)
	}
	sort.Strings(names)
	return names
}
