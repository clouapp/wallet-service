package models

import "testing"

func TestAccount_Role_VocabularyIsClosed(t *testing.T) {
	got := AccountRoles()
	want := []string{AccountRoleOwner, AccountRoleAdmin, AccountRoleAuditor, AccountRoleUser}
	if len(got) != len(want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roles = %v, want %v", got, want)
		}
	}
	if AccountRoleInRule() != "required|in:owner,admin,auditor,user" {
		t.Fatalf("rule = %q", AccountRoleInRule())
	}
	for _, role := range want {
		if !IsAccountRole(role) {
			t.Fatalf("%s should be an account role", role)
		}
	}
	if IsAccountRole(RetiredAccountRoleViewer) {
		t.Fatal("viewer is not an account role")
	}
	if IsAccountRole("") || IsAccountRole("super_admin") {
		t.Fatal("unknown roles must be rejected")
	}
}
