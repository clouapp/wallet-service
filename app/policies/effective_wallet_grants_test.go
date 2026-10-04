package policies

import (
	"slices"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestEffectiveWalletGrantsUnionsTheCatalogs(t *testing.T) {
	owner := catalogPermissionsOf(t, AccountRoleCatalog(), models.AccountRoleOwner)
	if !slices.Contains(owner, models.AccountPermWalletsRead) {
		t.Fatal("owner grant lacks wallets.read")
	}
	ownerAndViewer := EffectiveWalletGrants(models.AccountRoleOwner, []string{models.WalletRoleViewer})
	if !slices.Equal(ownerAndViewer, owner) {
		t.Fatalf("owner ∪ viewer = %v, want owner grant %v", ownerAndViewer, owner)
	}

	user := catalogPermissionsOf(t, AccountRoleCatalog(), models.AccountRoleUser)
	if !slices.Equal(user, []string{models.AccountPermAddressesCreate}) {
		t.Fatalf("account user catalog = %v, want [%s]", user, models.AccountPermAddressesCreate)
	}
	spender := walletRolePermissionsOf(t, models.WalletRoleSpender)
	userAndSpender := EffectiveWalletGrants(models.AccountRoleUser, []string{models.WalletRoleSpender})
	wantUserAndSpender := sortedUnion(user, spender)
	if !slices.Equal(userAndSpender, wantUserAndSpender) {
		t.Fatalf("user ∪ spender = %v, want %v", userAndSpender, wantUserAndSpender)
	}
	for _, permission := range []string{
		models.AccountPermAddressesCreate,
		models.AccountPermWithdrawalsCreate,
		models.AccountPermSweepExecute,
	} {
		if !slices.Contains(userAndSpender, permission) {
			t.Fatalf("user ∪ spender set missing %s", permission)
		}
	}
	// HTTP withdraw, sweep, and wallet create are not this function.
	// Those routes stay on the account-role fund guard.
	for _, action := range []string{FundWithdraw, FundSweep, FundCreateWallet} {
		if MayPerformFundAction(models.AccountRoleUser, action) {
			t.Fatalf("account user passes fund action %s", action)
		}
		if MayPerformFundAction(models.AccountRoleAuditor, action) {
			t.Fatalf("account auditor passes fund action %s", action)
		}
	}

	auditor := catalogPermissionsOf(t, AccountRoleCatalog(), models.AccountRoleAuditor)
	if len(auditor) == 0 {
		t.Fatal("auditor grant is empty")
	}
	admin := walletRolePermissionsOf(t, models.WalletRoleAdmin)
	auditorAndAdmin := EffectiveWalletGrants(models.AccountRoleAuditor, []string{models.WalletRoleAdmin})
	for _, permission := range auditor {
		if !slices.Contains(auditorAndAdmin, permission) {
			t.Fatalf("auditor ∪ admin dropped %s", permission)
		}
	}
	if !slices.Equal(auditorAndAdmin, sortedUnion(auditor, admin)) {
		t.Fatalf("auditor ∪ admin = %v, want %v", auditorAndAdmin, sortedUnion(auditor, admin))
	}

	withUnknown := EffectiveWalletGrants(models.AccountRoleUser, []string{models.WalletRoleSpender, "not-a-wallet-role"})
	if !slices.Equal(withUnknown, userAndSpender) {
		t.Fatalf("unknown wallet role added names: %v", withUnknown)
	}

	viewerAsAccount := EffectiveWalletGrants(models.WalletRoleViewer, nil)
	if len(viewerAsAccount) != 0 {
		t.Fatalf("wallet viewer as an account role = %v, want no names", viewerAsAccount)
	}
	unknownAccount := EffectiveWalletGrants("not-an-account-role", []string{models.WalletRoleSpender})
	if !slices.Equal(unknownAccount, spender) {
		t.Fatalf("unknown account role ∪ spender = %v, want %v", unknownAccount, spender)
	}
}

func walletRolePermissionsOf(t *testing.T, role string) []string {
	t.Helper()
	for _, entry := range WalletRoleCatalog() {
		if entry.Role == role {
			return entry.Permissions
		}
	}
	t.Fatalf("missing wallet role %s", role)
	return nil
}

func sortedUnion(parts ...[]string) []string {
	held := map[string]struct{}{}
	for _, part := range parts {
		for _, permission := range part {
			held[permission] = struct{}{}
		}
	}
	names := make([]string, 0, len(held))
	for permission := range held {
		names = append(names, permission)
	}
	slices.Sort(names)
	return names
}
