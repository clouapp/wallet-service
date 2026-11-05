package rules

import (
	"context"
	"testing"
)

// Bridge answers 400 "residential_address: cannot be a PO box" to a US
// customer whose street is a post office box (sandbox, 14/10/2026). Refusing
// it at the form names the field instead of failing in the worker.
func TestKycNotPoBoxRule_Passes(t *testing.T) {
	r := &KycNotPoBoxRule{}
	ctx := context.Background()
	usa := mapData{"country": "USA"}
	for _, tc := range []struct {
		name string
		data mapData
		line string
		want bool
	}{
		{"a street in the united states", usa, "123 Market St Apt 4", true},
		{"po box", usa, "PO Box 123", false},
		{"p.o. box", usa, "P.O. Box 123", false},
		{"pobox in lower case", usa, "pobox 9", false},
		{"post office box", usa, "Post Office Box 77", false},
		{"alpha-2 residence", mapData{"country": "us"}, "PO Box 123", false},
		{"a word that only contains the letters", usa, "12 Apollo Boxwood Rd", true},
		{"a po box outside the united states is not this rule's", mapData{"country": "PRT"}, "PO Box 123", true},
		{"no country reads as brazil", mapData{}, "PO Box 123", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Passes(ctx, tc.data, tc.line); got != tc.want {
				t.Errorf("Passes(%v, %q) = %v, want %v", tc.data, tc.line, got, tc.want)
			}
		})
	}
	if r.Passes(ctx, usa, 12) {
		t.Error("a number is not an address line")
	}
	if got := r.Message(ctx); got != "must be a physical address, not a PO Box" {
		t.Errorf("Message() = %q", got)
	}
}
