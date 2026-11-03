package ramps

import "testing"

// The name travels twice — once on the estimate, once on the create — and the
// two are compared byte for byte. A keyboard that appends a space to one of
// them turns a valid order into 409 estimate_mismatch, which is what happened
// in production on 04/09.
func TestNormalizeRecipientName(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"trims the trailing space a keyboard adds", "Maria Silva ", "Maria Silva"},
		{"trims both ends", "  Maria Silva  ", "Maria Silva"},
		{"collapses a run inside the name", "Maria  Silva", "Maria Silva"},
		{"collapses a tab and a newline too", "Maria\tSilva\n", "Maria Silva"},
		{"leaves a clean name alone", "Maria Silva", "Maria Silva"},
		{"whitespace only is empty", "   ", ""},
		{"empty stays empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeRecipientName(c.in); got != c.want {
				t.Fatalf("normalizeRecipientName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
