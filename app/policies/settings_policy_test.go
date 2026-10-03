package policies

import "testing"

func TestSettingsPermissionsFollowTheAccountRoles(t *testing.T) {
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
