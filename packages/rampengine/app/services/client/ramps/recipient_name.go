package ramps

import "strings"

// normalizeRecipientName collapses the whitespace in the PIX recipient's name.
//
// The name is the only free-text field the off-ramp binds on: it travels once
// on the estimate and again on the create, and bindOfframpCreate compares the
// two byte for byte. Whitespace is the one difference between them that no
// provider treats as meaningful and every keyboard produces — a mobile field
// that appends a space to "Maria Silva " turned a valid order into 409
// estimate_mismatch in production on 04/09.
//
// Normalizing here rather than only in the comparison is deliberate: the same
// value is what registers the payout destination upstream, and a destination
// whose owner carries a trailing space is a destination the provider may later
// fail to match against the bank's own record.
func normalizeRecipientName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}
