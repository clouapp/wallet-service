package rules

import (
	"context"
	"testing"

	"github.com/goravel/framework/contracts/validation"
)

func checkRule(t *testing.T, rule validation.Rule, cases map[string]bool) {
	t.Helper()
	for value, want := range cases {
		if got := rule.Passes(context.Background(), mapData{}, value); got != want {
			t.Errorf("%s(%q) = %v, want %v", rule.Signature(), value, got, want)
		}
	}
	if rule.Passes(context.Background(), mapData{}, 12) {
		t.Errorf("%s: a number is not a string", rule.Signature())
	}
}

func TestAbaRoutingRule(t *testing.T) {
	checkRule(t, &AbaRoutingRule{}, map[string]bool{
		"021000021": true, "101019644": true, "021000022": false, "02100002": false, "021-000021": false,
	})
}

func TestUsAccountNumberRule(t *testing.T) {
	checkRule(t, &UsAccountNumberRule{}, map[string]bool{
		"000123456789": true, "1234": true, "123": false, "12345678901234567890": false, "1234-5678": false,
	})
}

func TestBankOwnerNameRule(t *testing.T) {
	checkRule(t, &BankOwnerNameRule{}, map[string]bool{
		"Ada Lovelace":                 true,
		"  Leandro   Henriques Teste ": true,
		"Madonna":                      false,
		"Jo":                           false,
		"José Silva":                   false,
		"An Extremely Long Name That Goes On Forever": false,
	})
}

func TestUsStreetLineRule(t *testing.T) {
	checkRule(t, &UsStreetLineRule{}, map[string]bool{
		"123 Market St":       true,
		"923 Folsom Street":   true,
		"Market St 123":       false,
		"123 PO Box 9":        false,
		"12 P.O. Box 9":       false,
		"55 Main St PMB 1234": false,
		"":                    false,
	})
}

func TestUsStateRule(t *testing.T) {
	checkRule(t, &UsStateRule{}, map[string]bool{
		"CA": true, "ca": true, "DC": true, "TX": true, "XX": false, "California": false,
	})
}

func TestUsZipRule(t *testing.T) {
	checkRule(t, &UsZipRule{}, map[string]bool{
		"94103": true, "94103-0000": true, "941030000": true, "9410": false, "ABCDE": false,
	})
}

// Bridge refuses New York on a customer but takes it on an external account
// (sandbox, 05/11/2026), so a bank in New York is a valid payout destination.
func TestUsStateRuleAcceptsNewYorkForABankAccount(t *testing.T) {
	checkRule(t, &UsStateRule{}, map[string]bool{"NY": true, "ny": true, "XX": false})
}
