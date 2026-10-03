package policies

import "testing"

func TestActivityReadFollowsTheAccountRoles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role string
		read bool
	}{
		{role: "owner", read: true},
		{role: "admin", read: true},
		{role: "auditor", read: true},
		{role: "user", read: false},
		{role: "viewer", read: false},
		{role: "", read: false},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			t.Parallel()
			if got := MayReadActivity(tc.role); got != tc.read {
				t.Fatalf("MayReadActivity(%q) = %v, want %v", tc.role, got, tc.read)
			}
		})
	}
}
