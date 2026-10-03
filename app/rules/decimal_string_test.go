package rules

import (
	"context"
	"testing"
)

func TestDecimalStringAcceptsDecimals(t *testing.T) {
	rule := &DecimalString{}
	for _, value := range []any{"1.25", "0.000000000000000001", "1e-3", "", 7} {
		if !rule.Passes(context.Background(), nil, value) {
			t.Fatalf("%v: expected it to pass", value)
		}
	}
}

func TestDecimalStringRejectsNonFiniteAndHexValues(t *testing.T) {
	rule := &DecimalString{}
	for _, value := range []string{"NaN", "Inf", "-Inf", "infinity", "0x1p-2", "abc", "1,5", " ", " 1.5", "1.5 "} {
		if rule.Passes(context.Background(), nil, value) {
			t.Fatalf("%q: expected it to fail", value)
		}
	}
}
