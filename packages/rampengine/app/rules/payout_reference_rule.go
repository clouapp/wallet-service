package rules

import (
	"context"
	"regexp"
	"strings"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Bridge's limits on what travels with a USD payout to the bank: an ACH
// reference (destination_ach_reference) and a wire message
// (destination_wire_message).
var (
	achReferencePattern = regexp.MustCompile(`^[A-Za-z0-9 ]{1,10}$`)
	// A wire line is printable ASCII; lines are joined by \n (never \r).
	wireLinePattern = regexp.MustCompile(`^[\x20-\x7E]*$`)
)

const (
	// Bridge counts the whole message, line breaks included.
	wireMessageMaxLength = 140
	// Fedwire carries the message in 4 lines of 35 characters.
	wireMessageMaxLines   = 4
	wireMessageLineLength = 35
)

// validWireMessage reports whether message fits Fedwire: at most 140
// characters counting the \n, and its lines, each wrapped at 35 (an empty line
// still takes one), use at most 4 lines.
func validWireMessage(message string) bool {
	if message == "" || len(message) > wireMessageMaxLength {
		return false
	}
	used := 0
	for _, line := range strings.Split(message, "\n") {
		if !wireLinePattern.MatchString(line) {
			return false
		}
		used += max(1, (len(line)+wireMessageLineLength-1)/wireMessageLineLength)
	}
	return used <= wireMessageMaxLines
}

// PayoutReferenceRule is the reference of POST /api/offramps/address, held to
// the limit of the request's own rail. It is a rule rather than a regex in the
// request because the pattern depends on another field, and because a regex
// alternation's `|` is the rule separator.
type PayoutReferenceRule struct{}

func (r *PayoutReferenceRule) Signature() string { return "payout_reference" }

// Passes lets an absent reference through: it is optional, and "none" is a
// reference every rail takes.
func (r *PayoutReferenceRule) Passes(_ context.Context, data validation.Data, val any, _ ...any) bool {
	if val == nil {
		return true
	}
	reference, ok := val.(string)
	if !ok {
		return false
	}
	return ValidPayoutReference(stringFromData(data, "rail"), reference)
}

func (r *PayoutReferenceRule) Message(_ context.Context) string {
	return "must be at most 10 letters, digits or spaces for ACH, or for a wire at most 140 printable characters that fit 4 lines of 35"
}

// ValidPayoutReference reports whether reference fits rail. Empty fits every
// payout rail; a rail that pays no bank account takes no reference.
func ValidPayoutReference(rail, reference string) bool {
	switch rail {
	case models.RailUSDACHPayout:
		return reference == "" || achReferencePattern.MatchString(reference)
	case models.RailUSDWirePayout:
		return reference == "" || validWireMessage(reference)
	}
	return false
}
