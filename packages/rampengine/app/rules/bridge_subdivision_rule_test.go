package rules

import (
	"context"
	"testing"
)

func TestBridgeSubdivisionRule_Passes(t *testing.T) {
	r := &BridgeSubdivisionRule{}
	ctx := context.Background()

	t.Run("brazil", func(t *testing.T) {
		data := mapData{"country": "BRA"}
		valid := []string{"SP", "São Paulo", "sao paulo"}
		for _, v := range valid {
			if !r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be valid for BRA", v)
			}
		}
		invalid := []string{"ZZ", "CALIFORNIA"}
		for _, v := range invalid {
			if r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be rejected for BRA", v)
			}
		}
	})

	// The country is read the way the requiredness in the form request reads
	// it, so a padded or lower-case Brazil is still Brazil and an unknown
	// state is not waved through.
	t.Run("brazil named with padding or lowercase", func(t *testing.T) {
		for _, country := range []string{" BR ", "bra", " BRA"} {
			if r.Passes(ctx, mapData{"country": country}, "ZZ") {
				t.Errorf("expected ZZ to be rejected for %q", country)
			}
			if !r.Passes(ctx, mapData{"country": country}, "SP") {
				t.Errorf("expected SP to be valid for %q", country)
			}
		}
	})

	t.Run("brazil defaults when country empty", func(t *testing.T) {
		data := mapData{}
		if !r.Passes(ctx, data, "SP") {
			t.Error("expected SP to be valid when country is empty")
		}
	})

	t.Run("a country that requires a subdivision takes one from its list", func(t *testing.T) {
		data := mapData{"country": "ARE"}
		if !r.Passes(ctx, data, "DU") {
			t.Error("expected DU to be valid for ARE")
		}
		for _, v := range []string{"XX", "Dubai", "SP"} {
			if r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be rejected for ARE", v)
			}
		}
	})

	t.Run("a country that requires none ignores it", func(t *testing.T) {
		for _, v := range []string{"", "LIS", "Lisboa"} {
			if !r.Passes(ctx, mapData{"country": "PRT"}, v) {
				t.Errorf("expected %q to be ignored for PRT", v)
			}
		}
	})
	t.Run("the united states takes a state from its list, never new york", func(t *testing.T) {
		data := mapData{"country": "USA"}
		for _, v := range []string{"CA", "TX", "DC"} {
			if !r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be valid for USA", v)
			}
		}
		for _, v := range []string{"NY", "ca", "US-CA", "California", "XX"} {
			if r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be rejected for USA", v)
			}
		}
	})
}

// subdivision is required in Brazil (the UF) and wherever the country's
// address names one from a list.
func TestSubdivisionRequired(t *testing.T) {
	for country, want := range map[string]bool{
		"BRA": true, "": true, "bra": true, "ARE": true, "HKG": false,
		"PRT": false, "ARG": false, "USA": true, "us": true,
	} {
		if got := SubdivisionRequired(country); got != want {
			t.Errorf("SubdivisionRequired(%q) = %v, want %v", country, got, want)
		}
	}
}
