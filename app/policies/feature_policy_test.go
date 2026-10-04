package policies

import "testing"

func TestFeatureUpdatePermissionsAreNotAccountGrants(t *testing.T) {
	t.Parallel()

	if PermFeaturesUpdate == "" || PermFeaturesAccountUpdate == "" || PermFeaturesUpdate == PermFeaturesAccountUpdate {
		t.Fatal("features.update and features.account.update must both be declared and differ")
	}
	if PermFeaturesUpdate != "features.update" || PermFeaturesAccountUpdate != "features.account.update" {
		t.Fatal("feature write permissions must keep the S2.4 names")
	}
	if PermFeaturesUpdate == PermSettingsUpdate || PermFeaturesAccountUpdate == PermSettingsUpdate ||
		PermFeaturesUpdate == PermSettingsView || PermFeaturesAccountUpdate == PermSettingsView {
		t.Fatal("the feature write pair reused the settings pair")
	}
	for _, role := range []string{roleOwner, roleAdmin, roleAuditor, roleUser, "viewer", ""} {
		if Can(AccountRoleGrants(role), PermFeaturesUpdate) || Can(AccountRoleGrants(role), PermFeaturesAccountUpdate) {
			t.Fatalf("%s account grants hold a platform feature write permission", role)
		}
		if Can(WalletGrants(role), PermFeaturesUpdate) || Can(WalletGrants(role), PermFeaturesAccountUpdate) {
			t.Fatalf("%s wallet grants hold a platform feature write permission", role)
		}
	}
	for _, name := range APITokenPermissionCatalog() {
		if name == PermFeaturesUpdate || name == PermFeaturesAccountUpdate {
			t.Fatalf("api token catalog holds %s", name)
		}
	}
}
