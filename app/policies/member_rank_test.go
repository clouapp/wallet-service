package policies

import "testing"

func TestAccount_Role_Rank(t *testing.T) {
	owner, ok := AccountRoleRank(roleOwner)
	if !ok || owner != 3 {
		t.Fatalf("owner rank = %d ok=%v", owner, ok)
	}
	admin, ok := AccountRoleRank(roleAdmin)
	if !ok || admin != 2 {
		t.Fatalf("admin rank = %d ok=%v", admin, ok)
	}
	auditor, ok := AccountRoleRank(roleAuditor)
	if !ok || auditor != 1 {
		t.Fatalf("auditor rank = %d ok=%v", auditor, ok)
	}
	user, ok := AccountRoleRank(roleUser)
	if !ok || user != 1 {
		t.Fatalf("user rank = %d ok=%v", user, ok)
	}
	if _, ok := AccountRoleRank("viewer"); ok {
		t.Fatal("viewer has no rank on this branch")
	}
}

func TestMay_Grant_AdminCannotGrantOwner(t *testing.T) {
	if MayGrant(roleAdmin, roleOwner) {
		t.Fatal("admin must not grant owner")
	}
	if !MayGrant(roleAdmin, roleAdmin) {
		t.Fatal("admin may grant admin")
	}
	if !MayGrant(roleAdmin, roleUser) || !MayGrant(roleAdmin, roleAuditor) {
		t.Fatal("admin may grant auditor and user")
	}
	if !MayGrant(roleOwner, roleOwner) {
		t.Fatal("owner may grant owner")
	}
	if MayGrant(roleAuditor, roleAdmin) || MayGrant("viewer", roleUser) {
		t.Fatal("unknown and lower ranks must not grant upward")
	}
}

func TestMay_ActOn_CannotTouchAHigherRank(t *testing.T) {
	if MayActOn(roleAdmin, roleOwner) {
		t.Fatal("admin must not act on an owner")
	}
	if !MayActOn(roleAdmin, roleAdmin) || !MayActOn(roleAdmin, roleAuditor) || !MayActOn(roleAdmin, roleUser) {
		t.Fatal("admin may act on admin, auditor and user")
	}
	if !MayActOn(roleOwner, roleOwner) {
		t.Fatal("owner may act on another owner")
	}
}

func TestMemberRank_Manages_Members(t *testing.T) {
	if !ManagesMembers(roleOwner) || !ManagesMembers(roleAdmin) {
		t.Fatal("owner and admin manage members")
	}
	if ManagesMembers(roleAuditor) || ManagesMembers(roleUser) || ManagesMembers("viewer") {
		t.Fatal("auditor, user and unknown roles do not manage members")
	}
}
