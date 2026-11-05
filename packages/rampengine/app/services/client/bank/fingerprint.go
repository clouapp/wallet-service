package bank

import "github.com/macrowallets/waas/packages/rampengine/app/support/keyedhash"

// Fingerprint is the keyed digest the duplicate check compares: one per
// (routing, account) pair, never the numbers themselves. The separator keeps
// two splits of the same digits apart.
func Fingerprint(routing, account string) string {
	return keyedhash.Hex("us-bank-account:" + routing + ":" + account)
}

// Last4 is the last four digits of a normalized number.
func Last4(digits string) string {
	if len(digits) <= 4 {
		return digits
	}
	return digits[len(digits)-4:]
}

// Mask is how every response shows a number: its last four digits only.
func Mask(last4 string) string { return "••••" + last4 }
