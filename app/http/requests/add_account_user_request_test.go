package requests

import "testing"

func TestAddAccountUserRequestUsesOneRoleVocabulary(t *testing.T) {
	rule := (&AddAccountUserRequest{}).Rules(nil)["role"]
	const want = "required|in:owner,admin,auditor,user"
	if rule != want {
		t.Fatalf("role rule = %q, want %q", rule, want)
	}
}
