// Package digits reads the digits out of what a person types: a document, a
// bank number, a postal code. It is the one place that does it, whichever
// country the number is from; the packages that validate a number (brazil,
// usbank, ustaxid) call it instead of keeping a copy.
package digits

import "strings"

// Only keeps the ASCII digits of value: the spaces, dots and dashes a person
// types between groups are dropped.
func Only(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Last4 is the last four digits of value, "" when it has fewer than four.
func Last4(value string) string {
	d := Only(value)
	if len(d) < 4 {
		return ""
	}
	return d[len(d)-4:]
}
