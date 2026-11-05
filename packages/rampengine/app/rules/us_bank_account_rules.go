package rules

import (
	"context"
	"regexp"
	"strings"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
	"github.com/macrowallets/waas/packages/rampengine/app/support/usbank"
)

// The rules of a US bank account (POST /api/bank-accounts), as Bridge's
// external account takes it (external-accounts/validations and the create
// schema, checked 2026-10-14). Refusing at the form names the field; Bridge
// would refuse the same values later, inside the payout.

// AbaRoutingRule is an ABA routing number: nine digits, a Federal Reserve
// prefix and the checksum (usbank.ValidRouting).
type AbaRoutingRule struct{}

func (r *AbaRoutingRule) Signature() string { return "aba_routing" }

func (r *AbaRoutingRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	return ok && usbank.ValidRouting(s)
}

func (r *AbaRoutingRule) Message(_ context.Context) string {
	return "must be a valid 9-digit ABA routing number"
}

// UsAccountNumberRule is a US account number: 4 to 17 digits.
type UsAccountNumberRule struct{}

func (r *UsAccountNumberRule) Signature() string { return "us_account_number" }

func (r *UsAccountNumberRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	return ok && usbank.ValidAccountNumber(s)
}

func (r *UsAccountNumberRule) Message(_ context.Context) string {
	return "must be 4 to 17 digits"
}

// printableASCII is what Bridge takes in an ACH or wire owner name and in a
// wire message.
var printableASCII = regexp.MustCompile(`^[\x20-\x7E]+$`)

// BankOwnerNameRule is an account holder's name: 3 to 35 printable ASCII
// characters and at least two words, since Bridge requires a first and a last
// name of an individual.
type BankOwnerNameRule struct{}

func (r *BankOwnerNameRule) Signature() string { return "bank_owner_name" }

func (r *BankOwnerNameRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	name := usbank.NormalizeOwnerName(s)
	if len(name) < 3 || len(name) > 35 || !printableASCII.MatchString(name) {
		return false
	}
	_, _, twoWords := usbank.SplitOwnerName(name)
	return twoWords
}

func (r *BankOwnerNameRule) Message(_ context.Context) string {
	return "must be the holder's first and last name, 3 to 35 characters without accents"
}

// pmbPattern is a private mailbox, which Bridge refuses like a PO Box.
var pmbPattern = regexp.MustCompile(`(?i)\bPMB\b`)

// UsStreetLineRule is the first line of a US holder address: it starts with
// the street number Bridge requires of a US address, and it is not a PO Box
// or a private mailbox.
type UsStreetLineRule struct{}

func (r *UsStreetLineRule) Signature() string { return "us_street_line" }

func (r *UsStreetLineRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	line := strings.TrimSpace(s)
	if line == "" || line[0] < '0' || line[0] > '9' {
		return false
	}
	return !IsPoBox(line, "USA") && !pmbPattern.MatchString(line)
}

func (r *UsStreetLineRule) Message(_ context.Context) string {
	return "must start with the street number and not be a PO Box or a PMB"
}

// UsStateRule is a US state as Bridge spells it: a code from the USA list of
// iso3166 (the states Bridge serves, DC and the territories).
type UsStateRule struct{}

func (r *UsStateRule) Signature() string { return "us_state" }

func (r *UsStateRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	code := strings.ToUpper(strings.TrimSpace(s))
	// The shared list leaves New York out for Bridge's customers. Its external
	// accounts take it (sandbox, 05/11/2026).
	if code == "NY" {
		return true
	}
	for _, state := range iso3166.RequiredSubdivisions("USA") {
		if state.Code == code {
			return true
		}
	}
	return false
}

func (r *UsStateRule) Message(_ context.Context) string {
	return "must be a US state code"
}

// usZipPattern is a ZIP code: five digits, optionally a dash and four more.
var usZipPattern = regexp.MustCompile(`^\d{5}(?:-?\d{4})?$`)

// UsZipRule is a US ZIP code.
type UsZipRule struct{}

func (r *UsZipRule) Signature() string { return "us_zip" }

func (r *UsZipRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	return ok && usZipPattern.MatchString(strings.TrimSpace(s))
}

func (r *UsZipRule) Message(_ context.Context) string {
	return "must be a 5-digit ZIP code, optionally with 4 more"
}
