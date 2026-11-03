package policies

import "testing"

func TestRampPermissions_PrivateProfileAndWritesAreRestricted(t *testing.T) {
	for _, permission := range RampPermissions() {
		if !MayOperateRamps("owner", permission) || !MayOperateRamps("admin", permission) || MayOperateRamps("user", permission) {
			t.Fatalf("incorrect grants %s", permission)
		}
	}
	if MayOperateRamps("auditor", PermRampProfileRead) || MayOperateRamps("auditor", PermRampOrdersCreate) || MayOperateRamps("auditor", PermRampComplianceWrite) {
		t.Fatal("auditor can read identity or move funds")
	}
	if !MayOperateRamps("auditor", PermRampComplianceRead) {
		t.Fatal("auditor cannot inspect compliance status")
	}
}
