package policies

import "sort"

// EffectiveWalletGrants is the set union of AccountRoleCatalog grants for
// accountRole and WalletRoleCatalog grants for walletRoles. An unknown
// account role or an unknown wallet role contributes no names. Wallet
// viewer is not an account role. This is the catalog of effective names.
// It is not an HTTP gate: withdraw, sweep, and wallet create stay on the
// account-role fund guard, and this function is not consulted there.
func EffectiveWalletGrants(accountRole string, walletRoles []string) []string {
	held := map[string]struct{}{}
	rememberGrantNames(held, accountRoleCatalogPermissions(accountRole))
	for _, role := range walletRoles {
		rememberGrantNames(held, walletRoleCatalogPermissions(role))
	}
	names := make([]string, 0, len(held))
	for permission := range held {
		names = append(names, permission)
	}
	sort.Strings(names)
	return names
}

func accountRoleCatalogPermissions(role string) []string {
	for _, entry := range AccountRoleCatalog() {
		if entry.Role == role {
			return entry.Permissions
		}
	}
	return nil
}

func walletRoleCatalogPermissions(role string) []string {
	for _, entry := range WalletRoleCatalog() {
		if entry.Role == role {
			return entry.Permissions
		}
	}
	return nil
}

func rememberGrantNames(held map[string]struct{}, permissions []string) {
	for _, permission := range permissions {
		if permission == "" {
			continue
		}
		held[permission] = struct{}{}
	}
}
