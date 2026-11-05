// Package usbank reads the identifiers of a bank account in the United States
// the way every layer holds them: an ABA routing number and an account number
// as their digits, and the holder's name as Bridge's external account needs it.
package usbank

import (
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
	"github.com/macrowallets/waas/packages/rampengine/app/support/keyedhash"
)

// ValidRouting reports whether routing is an ABA routing number: nine digits,
// a Federal Reserve prefix (01-12, 21-32, 61-72 or 80) and the 3-7-1 checksum.
// Anything but digits in routing fails: it is checked as typed.
func ValidRouting(routing string) bool {
	if len(routing) != 9 || digits.Only(routing) != routing {
		return false
	}
	prefix := int(routing[0]-'0')*10 + int(routing[1]-'0')
	if !((prefix >= 1 && prefix <= 12) || (prefix >= 21 && prefix <= 32) ||
		(prefix >= 61 && prefix <= 72) || prefix == 80) {
		return false
	}
	weights := [9]int{3, 7, 1, 3, 7, 1, 3, 7, 1}
	sum := 0
	for i, w := range weights {
		sum += w * int(routing[i]-'0')
	}
	return sum%10 == 0
}

// ValidAccountNumber reports whether number is a US account number as Bridge
// takes it: 4 to 17 digits and nothing else.
func ValidAccountNumber(number string) bool {
	return len(number) >= 4 && len(number) <= 17 && digits.Only(number) == number
}

// SplitOwnerName collapses the spaces of name and splits it into the first
// and last names Bridge requires of an individual's external account: the last
// word is the last name, everything before it the first. ok is false for a
// name of fewer than two words.
func SplitOwnerName(name string) (first, last string, ok bool) {
	words := strings.Fields(name)
	if len(words) < 2 {
		return "", "", false
	}
	return strings.Join(words[:len(words)-1], " "), words[len(words)-1], true
}

// NormalizeOwnerName collapses the spaces of name: "  Ada   Lovelace " is
// "Ada Lovelace", the spelling stored and sent.
func NormalizeOwnerName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// AccountHash is the keyed digest a user's bank accounts are deduplicated on:
// the routing and account digits, so one account typed with or without
// spaces is one account. Keyed like every other identifier digest
// (keyedhash): the inputs are small enough to enumerate.
func AccountHash(routing, account string) string {
	return keyedhash.Hex(digits.Only(routing) + ":" + digits.Only(account))
}
