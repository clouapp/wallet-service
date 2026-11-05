package rules

import (
	"context"
	"testing"
)

func TestBridgePostalCodeRule_Passes(t *testing.T) {
	r := &BridgePostalCodeRule{}
	ctx := context.Background()

	t.Run("brazil", func(t *testing.T) {
		data := mapData{"country": "BRA"}
		valid := []string{"01310100", "01310-100", "01001-000", "01001000", " 01310100 "}
		for _, v := range valid {
			if !r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be valid for BRA", v)
			}
		}
		invalid := []string{"123", "123456789", "0100-1000"}
		for _, v := range invalid {
			if r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be rejected for BRA", v)
			}
		}
	})

	t.Run("brazil defaults when country empty", func(t *testing.T) {
		if !r.Passes(ctx, mapData{}, "01310-100") {
			t.Error("expected a CEP to be valid when country is empty")
		}
		if r.Passes(ctx, mapData{}, "1000-001") {
			t.Error("expected a non-CEP to be rejected when country is empty")
		}
	})

	t.Run("a country with a postal code format", func(t *testing.T) {
		data := mapData{"country": "PRT"}
		if !r.Passes(ctx, data, "1000-001") {
			t.Error("expected 1000-001 to be valid for PRT")
		}
		for _, v := range []string{"1000", "1000001", "01310-100"} {
			if r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be rejected for PRT", v)
			}
		}
	})

	// The UK, Canada, the Netherlands and Ireland spell their codes in
	// capitals, and the patterns only match those: a code typed in lower
	// case is the same code.
	t.Run("a code typed in lower case", func(t *testing.T) {
		for country, code := range map[string]string{
			"GBR": "sw1a 1aa", "CAN": " k1a 0b1 ", "NLD": "1012 jS", "IRL": "d02 x285",
		} {
			if !r.Passes(ctx, mapData{"country": country}, code) {
				t.Errorf("expected %q to be valid for %s", code, country)
			}
		}
		if r.Passes(ctx, mapData{"country": "GBR"}, "sw1a") {
			t.Error("expected a partial code to stay rejected for GBR")
		}
	})

	t.Run("a country without postal codes takes anything", func(t *testing.T) {
		if !r.Passes(ctx, mapData{"country": "ARE"}, "12345") {
			t.Error("expected any postal code to be valid for ARE")
		}
	})
}

// postal_code is required exactly where the country issues one.
func TestPostalCodeRequired(t *testing.T) {
	for country, want := range map[string]bool{
		"BRA": true, "": true, "PRT": true, "GBR": true, "ARG": true,
		"ARE": false, "HKG": false,
	} {
		if got := PostalCodeRequired(country); got != want {
			t.Errorf("PostalCodeRequired(%q) = %v, want %v", country, got, want)
		}
	}
}
