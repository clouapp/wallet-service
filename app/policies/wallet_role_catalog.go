package policies

import (
	"sort"

	"github.com/macrowallets/waas/app/models"
)

// WalletRoleGrant is one wallet role and the permissions that role holds.
// Viewer is a wallet role. These permissions are not account grants.
type WalletRoleGrant struct {
	Role        string
	Permissions []string
}

// WalletRoleCatalog lists admin, spender, approver, and viewer.
// admin holds wallets.write, wallet_users.write, whitelist.write, and
// webhooks.write. spender holds withdrawals.create, addresses.create, and
// sweep.execute. approver holds withdrawals.approve. viewer holds
// wallets.read. This catalog does not change Can or WalletCan, does not
// give the user role fund movement, and does not gate withdrawals.approve
// or roles.write.
func WalletRoleCatalog() []WalletRoleGrant {
	roles := []string{
		models.WalletRoleAdmin,
		models.WalletRoleSpender,
		models.WalletRoleApprover,
		models.WalletRoleViewer,
	}
	catalog := make([]WalletRoleGrant, 0, len(roles))
	for _, role := range roles {
		permissions := walletRolePermissions(role)
		if len(permissions) == 0 {
			panic("wallet role catalog: " + role + " has no grants")
		}
		catalog = append(catalog, WalletRoleGrant{
			Role:        role,
			Permissions: permissions,
		})
	}
	return catalog
}

func walletRolePermissions(role string) []string {
	var names []string
	switch role {
	case models.WalletRoleAdmin:
		names = []string{
			models.AccountPermWalletsWrite,
			models.AccountPermWalletUsersWrite,
			models.AccountPermWhitelistWrite,
			models.AccountPermWebhooksWrite,
		}
	case models.WalletRoleSpender:
		names = []string{
			models.AccountPermWithdrawalsCreate,
			models.AccountPermAddressesCreate,
			models.AccountPermSweepExecute,
		}
	case models.WalletRoleApprover:
		names = []string{models.AccountPermWithdrawalsApprove}
	case models.WalletRoleViewer:
		names = []string{models.AccountPermWalletsRead}
	default:
		return nil
	}
	sort.Strings(names)
	return names
}
