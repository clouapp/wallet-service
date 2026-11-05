package rules

import (
	"context"
	"testing"
)

func TestKycCountryRule_Passes(t *testing.T) {
	r := &KycCountryRule{}
	ctx := context.Background()

	if r.Signature() != "kyc_country" {
		t.Fatalf("Signature() = %q, want kyc_country", r.Signature())
	}
	tests := []struct {
		val  any
		want bool
	}{
		{"BRA", true},
		{"bra", true},
		{"br", true},
		{"PRT", true},
		{" ARG ", true},
		{"ZZZ", false},
		{"ZZ", false},
		{"BRAZIL", false},
		{"", false},
		{76, false},
	}
	for _, tt := range tests {
		if got := r.Passes(ctx, mapData{}, tt.val); got != tt.want {
			t.Errorf("Passes(%v) = %v, want %v", tt.val, got, tt.want)
		}
	}
	if r.Message(ctx) == "" {
		t.Error("expected a message")
	}
}
