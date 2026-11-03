package rules

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The birth date used to be checked by `regex:^\d{4}-\d{2}-\d{2}$` alone,
// which is a shape test and not a date test. Measured 01/09 against the live
// backend: 2030-01-01, 2020-01-01, 1800-01-01 and 1990-13-45 were all
// accepted and fanned out to both providers.
//
// Each of those is a different kind of wrong and only one of them is the
// user's typo, but all four reached the app the same way — as whatever
// rejection the provider chose to send back, mapped to a generic upstream
// error. The date is the one field on that form whose validity this side can
// decide by itself.
func TestKycBirthDateRule(t *testing.T) {
	rule := &KycBirthDateRule{}
	require.Equal(t, "kyc_birth_date", rule.Signature())

	today := time.Now().UTC()
	adult := today.AddDate(-30, 0, 0).Format("2006-01-02")
	exactly18 := today.AddDate(-18, 0, 0).Format("2006-01-02")
	dayShyOf18 := today.AddDate(-18, 0, 1).Format("2006-01-02")
	tomorrow := today.AddDate(0, 0, 1).Format("2006-01-02")

	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"an ordinary adult", adult, true},
		{"eighteen today", exactly18, true},
		{"a day short of eighteen", dayShyOf18, false},
		{"a minor", today.AddDate(-6, 0, 0).Format("2006-01-02"), false},
		{"tomorrow", tomorrow, false},
		{"a year in the future", today.AddDate(5, 0, 0).Format("2006-01-02"), false},
		// Nobody alive was born then, and a date this far back is a typo or a
		// broken client, never a customer.
		{"eighteen hundred", "1800-01-01", false},
		// The regex admitted these: month 13, day 45, and 29 February in a
		// year that has no 29 February.
		{"month thirteen", "1990-13-05", false},
		{"day forty-five", "1990-01-45", false},
		{"february thirtieth", "1990-02-30", false},
		{"not a leap year", "1991-02-29", false},
		{"a real leap day", "1992-02-29", true},
		// Shape failures the regex already caught stay failures.
		{"slashes", "02/04/1990", false},
		{"no padding", "1990-4-2", false},
		{"empty", "", false},
		{"words", "yesterday", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rule.Passes(context.Background(), mapData{}, tc.value)
			require.Equal(t, tc.want, got, "value %q", tc.value)
		})
	}
}

// A non-string never passes: the rule is asked about a form field, and
// anything else reaching it is a wiring mistake, not a valid date.
func TestKycBirthDateRuleRejectsNonStrings(t *testing.T) {
	rule := &KycBirthDateRule{}
	for _, val := range []any{nil, 19900402, 1990.0, true} {
		require.False(t, rule.Passes(context.Background(), mapData{}, val),
			fmt.Sprintf("%T must not pass", val))
	}
}

// The message has to name what to fix. "Invalid" would send the user back to
// a field they cannot see anything wrong with.
func TestKycBirthDateRuleMessageNamesTheConstraint(t *testing.T) {
	msg := (&KycBirthDateRule{}).Message(context.Background())
	require.Contains(t, msg, "18")
	require.Contains(t, msg, "YYYY-MM-DD")
}
