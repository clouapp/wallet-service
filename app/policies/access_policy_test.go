package policies

import (
	"testing"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
)

func TestCan_Users_ReadFailsClosedAndFollowsTheRoleCatalog(t *testing.T) {
	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleAuditor, models.RetiredAccountRoleViewer} {
		if !Can(AccountRoleGrants(role), PermUsersRead) {
			t.Fatalf("%s must hold users.read", role)
		}
	}
	for _, role := range []string{models.AccountRoleUser, "", "spender", "owner "} {
		if Can(AccountRoleGrants(role), PermUsersRead) {
			t.Fatalf("%q must not hold users.read", role)
		}
	}
	owner := AccountRoleGrants(models.AccountRoleOwner)
	if Can(owner, "") || Can(owner, "users.delete") || Can(nil, PermUsersRead) || Can(Grants{}, PermUsersRead) {
		t.Fatal("empty permission, a permission outside the set, and an empty grant set are false")
	}
}

func TestCan_Account_LifecycleIsOwnerOnly(t *testing.T) {
	if !Can(AccountRoleGrants(models.AccountRoleOwner), PermAccountLifecycle) {
		t.Fatal("owner must hold account.lifecycle")
	}
	for _, role := range []string{models.AccountRoleAdmin, models.AccountRoleAuditor, models.RetiredAccountRoleViewer, models.AccountRoleUser, "", "spender", "owner "} {
		if Can(AccountRoleGrants(role), PermAccountLifecycle) {
			t.Fatalf("%q must not hold account.lifecycle", role)
		}
	}
}

func TestCan_Tokens_ReadIsOwnerAdminAndAuditor(t *testing.T) {
	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleAuditor} {
		if !Can(AccountRoleGrants(role), PermTokensRead) {
			t.Fatalf("%s must hold tokens.read", role)
		}
	}
	if Can(AccountRoleGrants(models.AccountRoleAuditor), PermTokensWrite) {
		t.Fatal("auditor account catalog must not hold tokens.write")
	}
	for _, role := range []string{models.RetiredAccountRoleViewer, models.AccountRoleUser, "", "spender", "owner "} {
		if Can(AccountRoleGrants(role), PermTokensRead) || Can(AccountRoleGrants(role), PermTokensWrite) {
			t.Fatalf("%q must not hold tokens.read or tokens.write", role)
		}
	}
}

func TestCan_Tokens_WriteIsOwnerAndAdmin(t *testing.T) {
	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin} {
		if !Can(AccountRoleGrants(role), PermTokensWrite) {
			t.Fatalf("%s must hold tokens.write", role)
		}
	}
	for _, role := range []string{models.AccountRoleAuditor, models.RetiredAccountRoleViewer, models.AccountRoleUser, "", "spender", "owner "} {
		if Can(AccountRoleGrants(role), PermTokensWrite) {
			t.Fatalf("%q must not hold tokens.write", role)
		}
	}
	user := AccountRoleGrants(models.AccountRoleUser)
	if len(user) != 1 || !Can(user, PermAddressesCreate) {
		t.Fatal("user account catalog must stay addresses.create only")
	}
}

func TestCan_Account_WriteIsOwnerAndAdmin(t *testing.T) {
	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin} {
		if !Can(AccountRoleGrants(role), PermAccountWrite) {
			t.Fatalf("%s must hold account.write", role)
		}
	}
	for _, role := range []string{models.AccountRoleAuditor, models.RetiredAccountRoleViewer, models.AccountRoleUser, "", "spender", "owner "} {
		if Can(AccountRoleGrants(role), PermAccountWrite) {
			t.Fatalf("%q must not hold account.write", role)
		}
	}
}

func TestCan_Users_WriteIsOwnerAndAdmin(t *testing.T) {
	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin} {
		if !Can(AccountRoleGrants(role), PermUsersWrite) {
			t.Fatalf("%s must hold users.write", role)
		}
		if !Can(AccountRoleGrants(role), PermUsersRead) {
			t.Fatalf("%s must still hold users.read", role)
		}
	}
	for _, role := range []string{models.AccountRoleAuditor, models.RetiredAccountRoleViewer, models.AccountRoleUser, "", "spender", "owner "} {
		if Can(AccountRoleGrants(role), PermUsersWrite) {
			t.Fatalf("%q must not hold users.write", role)
		}
	}
}

func TestWallet_Grants_AddressCreateAndRefusesFundMovement(t *testing.T) {
	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleUser} {
		if !Can(WalletGrants(role), PermAddressesCreate) {
			t.Fatalf("%s must hold addresses.create", role)
		}
	}
	for _, role := range []string{models.AccountRoleAuditor, models.RetiredAccountRoleViewer, "", "spender", "owner "} {
		if Can(WalletGrants(role), PermAddressesCreate) {
			t.Fatalf("%q must not hold addresses.create", role)
		}
	}
	user := WalletGrants(models.AccountRoleUser)
	for _, permission := range []string{PermWithdrawalsCreate, PermSweepExecute, PermWalletsCreate, PermUsersRead, PermUsersWrite, ""} {
		if Can(user, permission) {
			t.Fatalf("user wallet grants must not hold %q", permission)
		}
	}
	if Can(nil, PermAddressesCreate) || Can(Grants{}, PermAddressesCreate) {
		t.Fatal("an empty grant set does not hold addresses.create")
	}
	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleUser} {
		if !Can(AccountRoleGrants(role), PermAddressesCreate) {
			t.Fatalf("%s account grants must hold addresses.create", role)
		}
	}
	for _, role := range []string{models.AccountRoleAuditor, models.RetiredAccountRoleViewer, "", "spender"} {
		if Can(AccountRoleGrants(role), PermAddressesCreate) {
			t.Fatalf("%q account grants must not hold addresses.create", role)
		}
	}
	userAccount := AccountRoleGrants(models.AccountRoleUser)
	for _, permission := range []string{PermUsersRead, PermUsersWrite, PermSettingsRead, PermSettingsWrite, PermRolesRead, PermWithdrawalsCreate, PermSweepExecute, PermWalletsCreate, ""} {
		if Can(userAccount, permission) {
			t.Fatalf("user account grants must not hold %q", permission)
		}
	}
	if Can(AccountRoleGrants(models.AccountRoleOwner), PermWithdrawalsCreate) {
		t.Fatal("the account catalog does not grant fund movement")
	}
}

func TestMay_Grant_DoesNotAllowARoleAboveTheActor(t *testing.T) {
	if !MayGrant(models.AccountRoleOwner, models.AccountRoleOwner) {
		t.Fatal("owner may grant owner")
	}
	if !MayGrant(models.AccountRoleAdmin, models.AccountRoleAdmin) {
		t.Fatal("admin may grant an equal role")
	}
	if !MayGrant(models.AccountRoleAdmin, models.AccountRoleUser) || !MayGrant(models.AccountRoleAdmin, models.AccountRoleAuditor) {
		t.Fatal("admin may grant user and auditor")
	}
	if MayGrant(models.AccountRoleAdmin, models.AccountRoleOwner) {
		t.Fatal("admin must not grant owner")
	}
	if !MayGrant(models.AccountRoleUser, models.AccountRoleAuditor) || !MayGrant(models.AccountRoleAuditor, models.AccountRoleUser) {
		t.Fatal("user and auditor are the same rank")
	}
	if MayGrant(models.AccountRoleUser, models.AccountRoleAdmin) || MayGrant(models.AccountRoleAuditor, models.AccountRoleOwner) {
		t.Fatal("a lower role must not grant above itself")
	}
	if MayGrant("", models.AccountRoleUser) || MayGrant(models.AccountRoleOwner, "viewer") || MayGrant(models.AccountRoleOwner, "") {
		t.Fatal("unknown roles are not grantable")
	}
}

func TestMay_Act_OnUsesTheSameRankAndRemovalRefusesSelfAndLastOwner(t *testing.T) {
	if !MayActOn(models.AccountRoleAdmin, models.AccountRoleAdmin) {
		t.Fatal("admin may act on another admin")
	}
	if MayActOn(models.AccountRoleAdmin, models.AccountRoleOwner) {
		t.Fatal("admin must not act on an owner")
	}
	if !MayActOn(models.AccountRoleOwner, models.AccountRoleOwner) {
		t.Fatal("an owner may act on another owner")
	}

	actor := uuid.New()
	other := uuid.New()
	if err := RefuseMemberRemoval(actor, actor, models.AccountRoleOwner, models.AccountRoleOwner, 2); err != ErrCannotRemoveSelf {
		t.Fatalf("self removal = %v", err)
	}
	if err := RefuseMemberRemoval(actor, other, models.AccountRoleAdmin, models.AccountRoleOwner, 1); err != ErrCannotActOnMember {
		t.Fatalf("admin removing owner = %v", err)
	}
	if err := RefuseMemberRemoval(actor, other, models.AccountRoleOwner, models.AccountRoleOwner, 1); err != ErrLastOwner {
		t.Fatalf("last owner = %v", err)
	}
	if err := RefuseMemberRemoval(actor, other, models.AccountRoleOwner, models.AccountRoleOwner, 2); err != nil {
		t.Fatalf("second owner = %v", err)
	}
	if err := RefuseMemberRemoval(actor, other, models.AccountRoleAdmin, models.AccountRoleAdmin, 1); err != nil {
		t.Fatalf("admin removing admin = %v", err)
	}
}
