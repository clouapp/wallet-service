package policies

import "testing"

func TestAPI_Token_IPAllowsBlankKeepsTodaysAccess(t *testing.T) {
	t.Parallel()

	for _, stored := range []string{"", "   "} {
		if !APITokenIPAllows(stored, "203.0.113.8") {
			t.Fatalf("blank allowlist %q refused a client", stored)
		}
		if !APITokenIPAllows(stored, "") {
			t.Fatalf("blank allowlist %q refused an empty client", stored)
		}
	}
}

func TestAPI_Token_IPAllowsMatchingPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		stored string
		client string
		want   bool
	}{
		{stored: "192.0.2.0/24", client: "192.0.2.1", want: true},
		{stored: "192.0.2.0/24", client: "198.51.100.4", want: false},
		{stored: "192.0.2.1", client: "192.0.2.1", want: true},
		{stored: "192.0.2.1", client: "192.0.2.2", want: false},
		{stored: "192.0.2.1/32", client: "192.0.2.1", want: true},
		{stored: "10.0.0.0/8, 192.0.2.0/24", client: "192.0.2.9", want: true},
		{stored: "10.0.0.0/8, 192.0.2.0/24", client: "198.51.100.1", want: false},
		{stored: "2001:db8::/32", client: "2001:db8::1", want: true},
		{stored: "2001:db8::/32", client: "2001:db9::1", want: false},
		{stored: "::ffff:192.0.2.1", client: "192.0.2.1", want: true},
		{stored: "not-a-cidr", client: "192.0.2.1", want: false},
		{stored: "192.0.2.0/24,", client: "192.0.2.1", want: false},
		{stored: "192.0.2.0/24", client: "", want: false},
		{stored: "192.0.2.0/24", client: "not-an-ip", want: false},
	}
	for _, tc := range cases {
		if got := APITokenIPAllows(tc.stored, tc.client); got != tc.want {
			t.Errorf("APITokenIPAllows(%q, %q) = %v, want %v", tc.stored, tc.client, got, tc.want)
		}
	}
}

func TestValid_API_TokenIPCIDR(t *testing.T) {
	t.Parallel()

	for _, stored := range []string{"", "  ", "10.0.0.0/8", "192.0.2.1", "2001:db8::1", "10.0.0.0/8, 192.0.2.1/32"} {
		if !ValidAPITokenIPCIDR(stored) {
			t.Errorf("ValidAPITokenIPCIDR(%q) = false, want true", stored)
		}
	}
	for _, stored := range []string{"nope", "10.0.0.0/33", "10.0.0.0/8,", "192.0.2.1/24, ", ","} {
		if ValidAPITokenIPCIDR(stored) {
			t.Errorf("ValidAPITokenIPCIDR(%q) = true, want false", stored)
		}
	}
}
