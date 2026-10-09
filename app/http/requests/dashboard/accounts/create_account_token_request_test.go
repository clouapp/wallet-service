package accounts

import (
	"reflect"
	"testing"
)

func TestCreate_Account_TokenRequestChecksTheRestrictionsAfterTheRules(t *testing.T) {
	const badCIDR = "The ip_cidr must be a valid CIDR."
	const badObject = "must be an object with an optional daily_usd decimal"
	const negative = "must be a decimal string greater than or equal to 0"

	cases := []struct {
		name string
		req  CreateAccountTokenRequest
		want map[string][]string
	}{
		{name: "no restriction", req: CreateAccountTokenRequest{Name: "ci"}},
		{name: "a CIDR list", req: CreateAccountTokenRequest{IpCidr: "192.0.2.0/24, 2001:db8::/32"}},
		{name: "a daily cap", req: CreateAccountTokenRequest{SpendingLimit: map[string]any{"daily_usd": "12.50"}}},
		{name: "a blank cap", req: CreateAccountTokenRequest{SpendingLimit: map[string]any{}}},
		{name: "not a CIDR", req: CreateAccountTokenRequest{IpCidr: "not-a-cidr"}, want: map[string][]string{"ip_cidr": {badCIDR}}},
		{name: "a negative cap", req: CreateAccountTokenRequest{SpendingLimit: map[string]any{"daily_usd": "-1"}}, want: map[string][]string{"spending_limit.daily_usd": {negative}}},
		{name: "a negative amount elsewhere", req: CreateAccountTokenRequest{SpendingLimit: map[string]any{"weekly": "-5"}}, want: map[string][]string{"spending_limit.daily_usd": {negative}}},
		{name: "a cap that is not a decimal", req: CreateAccountTokenRequest{SpendingLimit: map[string]any{"daily_usd": "lots"}}, want: map[string][]string{"spending_limit": {badObject}}},
		{
			name: "the CIDR is answered before the cap",
			req:  CreateAccountTokenRequest{IpCidr: "nope", SpendingLimit: map[string]any{"daily_usd": "-1"}},
			want: map[string][]string{"ip_cidr": {badCIDR}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.req.After(nil)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("After = %v, want %v", got, tc.want)
			}
		})
	}
}
