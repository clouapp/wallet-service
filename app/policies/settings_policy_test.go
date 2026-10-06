package policies

import "testing"

func TestMail_Credential_PermissionsAreNotAccountGrants(t *testing.T) {
	t.Parallel()

	if PermMailView == PermSettingsView || PermMailUpdate == PermSettingsUpdate {
		t.Fatal("a mail credential permission reused the settings route pair")
	}
	for _, role := range []string{roleOwner, roleAdmin, roleAuditor, roleUser, "viewer", ""} {
		if Can(AccountRoleGrants(role), PermMailView) || Can(AccountRoleGrants(role), PermMailUpdate) {
			t.Fatalf("%s account grants hold a mail credential permission", role)
		}
		if Can(WalletGrants(role), PermMailView) || Can(WalletGrants(role), PermMailUpdate) {
			t.Fatalf("%s wallet grants hold a mail credential permission", role)
		}
	}
	for _, name := range APITokenPermissionCatalog() {
		if name == PermMailView || name == PermMailUpdate {
			t.Fatalf("api token catalog holds %s", name)
		}
	}
}

func TestProvider_Credential_PermissionsAreNotAccountGrants(t *testing.T) {
	t.Parallel()

	if PermProvidersView == PermSettingsView || PermProvidersUpdate == PermSettingsUpdate ||
		PermProvidersView == PermSettingsUpdate || PermProvidersUpdate == PermSettingsView {
		t.Fatal("a provider credential permission reused the settings route pair")
	}
	for _, role := range []string{roleOwner, roleAdmin, roleAuditor, roleUser, "viewer", ""} {
		if MayUpdateSettings(role) && (Can(AccountRoleGrants(role), PermProvidersView) || Can(AccountRoleGrants(role), PermProvidersUpdate)) {
			t.Fatalf("%s holds settings.update and a provider credential permission", role)
		}
		if Can(AccountRoleGrants(role), PermProvidersView) || Can(AccountRoleGrants(role), PermProvidersUpdate) {
			t.Fatalf("%s account grants hold a provider credential permission", role)
		}
		if Can(WalletGrants(role), PermProvidersView) || Can(WalletGrants(role), PermProvidersUpdate) {
			t.Fatalf("%s wallet grants hold a provider credential permission", role)
		}
	}
	for _, name := range APITokenPermissionCatalog() {
		if name == PermProvidersView || name == PermProvidersUpdate {
			t.Fatalf("api token catalog holds %s", name)
		}
	}
}

func TestSweep_Permissions_AreNotAccountGrants(t *testing.T) {
	t.Parallel()

	if PermSweepView == PermSettingsView || PermSweepUpdate == PermSettingsUpdate ||
		PermSweepView == PermSettingsUpdate || PermSweepUpdate == PermSettingsView {
		t.Fatal("holding settings.update does not by itself become sweep.update")
	}
	if PermSweepView == "chains.view" || PermSweepUpdate == "chains.update" ||
		PermSweepView == "chains.update" || PermSweepUpdate == "chains.view" {
		t.Fatal("the sweep pair reused the chain pair")
	}
	for _, role := range []string{roleOwner, roleAdmin, roleAuditor, roleUser, "viewer", ""} {
		if MayUpdateSettings(role) && (Can(AccountRoleGrants(role), PermSweepView) || Can(AccountRoleGrants(role), PermSweepUpdate)) {
			t.Fatalf("%s holds settings.update and a sweep permission", role)
		}
		if Can(AccountRoleGrants(role), PermSweepView) || Can(AccountRoleGrants(role), PermSweepUpdate) {
			t.Fatalf("%s account grants hold a sweep permission", role)
		}
		if Can(WalletGrants(role), PermSweepView) || Can(WalletGrants(role), PermSweepUpdate) {
			t.Fatalf("%s wallet grants hold a sweep permission", role)
		}
	}
	for _, name := range APITokenPermissionCatalog() {
		if name == PermSweepView || name == PermSweepUpdate {
			t.Fatalf("api token catalog holds %s", name)
		}
	}
}

func TestChain_Permissions_AreNotAccountGrants(t *testing.T) {
	t.Parallel()

	if PermChainsView == PermSettingsView || PermChainsUpdate == PermSettingsUpdate ||
		PermChainsView == PermSettingsUpdate || PermChainsUpdate == PermSettingsView {
		t.Fatal("holding settings.update does not by itself become chains.update")
	}
	if PermChainsView == PermSweepView || PermChainsUpdate == PermSweepUpdate ||
		PermChainsView == PermSweepUpdate || PermChainsUpdate == PermSweepView {
		t.Fatal("the chain pair reused the sweep pair")
	}
	if PermChainsView == "" || PermChainsUpdate == "" || PermChainsView == PermChainsUpdate {
		t.Fatal("chains.view and chains.update must both be declared and differ")
	}
	for _, role := range []string{roleOwner, roleAdmin, roleAuditor, roleUser, "viewer", ""} {
		if MayUpdateSettings(role) && (Can(AccountRoleGrants(role), PermChainsView) || Can(AccountRoleGrants(role), PermChainsUpdate)) {
			t.Fatalf("%s holds settings.update and a chain permission", role)
		}
		if Can(AccountRoleGrants(role), PermChainsView) || Can(AccountRoleGrants(role), PermChainsUpdate) {
			t.Fatalf("%s account grants hold a chain permission", role)
		}
		if Can(WalletGrants(role), PermChainsView) || Can(WalletGrants(role), PermChainsUpdate) {
			t.Fatalf("%s wallet grants hold a chain permission", role)
		}
	}
	for _, name := range APITokenPermissionCatalog() {
		if name == PermChainsView || name == PermChainsUpdate {
			t.Fatalf("api token catalog holds %s", name)
		}
	}
}

func TestAccount_Settings_GuardFollowsTheRoles(t *testing.T) {
	t.Parallel()

	if PermSettingsRead == PermSettingsView || PermSettingsWrite == PermSettingsUpdate ||
		PermSettingsRead == PermSettingsUpdate || PermSettingsWrite == PermSettingsView ||
		PermSettingsRead == "" || PermSettingsWrite == "" || PermSettingsRead == PermSettingsWrite {
		t.Fatal("settings.read and settings.write must be declared apart from the platform pair")
	}
	for _, role := range []string{roleOwner, roleAdmin} {
		if !Can(AccountRoleGrants(role), PermSettingsRead) || !Can(AccountRoleGrants(role), PermSettingsWrite) {
			t.Fatalf("%s must hold settings.read and settings.write", role)
		}
		if !MayViewSettings(role) || !MayUpdateSettings(role) {
			t.Fatalf("%s must still read and write account settings", role)
		}
	}
	if !Can(AccountRoleGrants(roleAuditor), PermSettingsRead) || Can(AccountRoleGrants(roleAuditor), PermSettingsWrite) {
		t.Fatal("auditor holds settings.read and must not hold settings.write")
	}
	if !MayViewSettings(roleAuditor) || MayUpdateSettings(roleAuditor) {
		t.Fatal("auditor must still read and must not write")
	}
	for _, role := range []string{roleUser, "", "spender", "owner "} {
		if Can(AccountRoleGrants(role), PermSettingsRead) || Can(AccountRoleGrants(role), PermSettingsWrite) {
			t.Fatalf("%q must not hold the account settings guard", role)
		}
		if MayViewSettings(role) || MayUpdateSettings(role) {
			t.Fatalf("%q must not read or write account settings", role)
		}
	}
	if MayViewSettings("viewer") || MayUpdateSettings("viewer") {
		t.Fatal("the retired viewer label stays refused by the live settings gate")
	}
	const optionalSecurityWrite = "settings.security.write"
	for _, role := range []string{roleOwner, roleAdmin, roleAuditor, roleUser, "viewer", ""} {
		if Can(AccountRoleGrants(role), optionalSecurityWrite) {
			t.Fatalf("%s holds settings.security.write", role)
		}
	}
	for _, name := range APITokenPermissionCatalog() {
		if name == PermSettingsRead || name == PermSettingsWrite || name == optionalSecurityWrite {
			t.Fatalf("api token catalog holds %s", name)
		}
	}
}

func TestSettings_Permissions_FollowTheAccountRoles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role   string
		view   bool
		update bool
	}{
		{role: "owner", view: true, update: true},
		{role: "admin", view: true, update: true},
		{role: "auditor", view: true, update: false},
		{role: "user", view: false, update: false},
		{role: "viewer", view: false, update: false},
		{role: "", view: false, update: false},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			t.Parallel()
			if got := MayViewSettings(tc.role); got != tc.view {
				t.Fatalf("MayViewSettings(%q) = %v, want %v", tc.role, got, tc.view)
			}
			if got := MayUpdateSettings(tc.role); got != tc.update {
				t.Fatalf("MayUpdateSettings(%q) = %v, want %v", tc.role, got, tc.update)
			}
		})
	}
}
