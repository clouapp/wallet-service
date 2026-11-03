package bridge

import "testing"

func TestNormalizeSubdivisionBrazil(t *testing.T) {
	if got := normalizeSubdivision("BRA", "Minas Gerais"); got != "MG" {
		t.Fatalf("normalizeSubdivision = %q, want MG", got)
	}
	if got := normalizeSubdivision("BRA", "kg"); got != "kg" {
		t.Fatalf("normalizeSubdivision invalid = %q, want kg passthrough", got)
	}
}
