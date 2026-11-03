// Package bank is the bank-account side of the off-ramp that no provider
// owns: the customer's saved US bank beneficiaries, validated, fingerprinted
// and masked here before anything is stored, and only E4 sends them out.
package bank

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/bank"
)

const (
	AccountClassIndividual = "individual"
	AccountClassBusiness   = "business"
)

// ErrInvalidBeneficiary is what every FieldError unwraps to, so a caller can
// tell a refusal of the data from a fault with errors.Is.
var ErrInvalidBeneficiary = errors.New("bank: invalid beneficiary")

// FieldError names the first field Validate refused, in the shape
// responses.FieldError answers.
type FieldError struct {
	Field, Rule, Message string
}

func (e *FieldError) Error() string { return "bank: " + e.Field + ": " + e.Message }
func (e *FieldError) Unwrap() error { return ErrInvalidBeneficiary }

var (
	accountClasses = set("individual", "business")
	accountTypes   = set("checking", "saving")
	// recipient_relationship as BlindPay lists it: first_party and the nine
	// third-party relations (spec §4.5).
	relationships = set("first_party", "employee", "independent_contractor", "vendor_or_supplier",
		"subsidiary_or_affiliate", "merchant_or_partner", "customer", "landlord", "family", "other")
	// The 50 states and DC. Territories are not states for a US bank payout.
	usStates = set("AL", "AK", "AZ", "AR", "CA", "CO", "CT", "DE", "DC", "FL", "GA", "HI", "ID", "IL", "IN",
		"IA", "KS", "KY", "LA", "ME", "MD", "MA", "MI", "MN", "MS", "MO", "MT", "NE", "NV", "NH", "NJ", "NM",
		"NY", "NC", "ND", "OH", "OK", "OR", "PA", "RI", "SC", "SD", "TN", "TX", "UT", "VT", "VA", "WA", "WV",
		"WI", "WY")
	zipCode    = regexp.MustCompile(`^\d{5}(-\d{4})?$`)
	digitsOnly = regexp.MustCompile(`^\d+$`)
)

// stripSeparators drops what a user pastes between the digits of a number:
// every Unicode space (NBSP included) and the hyphen and dash variants
// U+002D, U+2010–U+2014. Dots and slashes stay, so they are refused.
func stripSeparators(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' || r >= '\u2010' && r <= '\u2014' {
			return -1
		}
		return r
	}, s)
}

func set(values ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(values))
	for _, v := range values {
		m[v] = struct{}{}
	}
	return m
}

// Normalize trims every field, drops spaces and dashes from the routing and
// account numbers as a user pastes them, upper-cases the state, and drops the
// business industry of an individual account.
func Normalize(in dto.BeneficiaryDTO) dto.BeneficiaryDTO {
	out := dto.BeneficiaryDTO{
		Label: strings.TrimSpace(in.Label), AccountClass: strings.TrimSpace(in.AccountClass),
		AccountType: strings.TrimSpace(in.AccountType), Relationship: strings.TrimSpace(in.Relationship),
		BeneficiaryName: strings.TrimSpace(in.BeneficiaryName),
		RoutingNumber:   stripSeparators(strings.TrimSpace(in.RoutingNumber)),
		AccountNumber:   stripSeparators(strings.TrimSpace(in.AccountNumber)),
		AddressLine1:    strings.TrimSpace(in.AddressLine1), AddressLine2: strings.TrimSpace(in.AddressLine2),
		City: strings.TrimSpace(in.City), StateCode: strings.ToUpper(strings.TrimSpace(in.StateCode)),
		PostalCode: strings.TrimSpace(in.PostalCode), BusinessIndustry: strings.TrimSpace(in.BusinessIndustry),
	}
	if out.AccountClass != AccountClassBusiness {
		out.BusinessIndustry = ""
	}
	return out
}

// Validate checks a normalized beneficiary and answers the first field it
// refuses, in the order the app's form shows them.
func Validate(in dto.BeneficiaryDTO) error {
	switch {
	case in.Label == "" || utf8.RuneCountInString(in.Label) > 64:
		return invalid("label", "max_len", "label is required and at most 64 characters")
	case !has(accountClasses, in.AccountClass):
		return invalid("account_class", "in", "account_class must be individual or business")
	case !has(accountTypes, in.AccountType):
		return invalid("account_type", "in", "account_type must be checking or saving")
	case !has(relationships, in.Relationship):
		return invalid("relationship", "in", "relationship is not one a US bank payout accepts")
	case in.BeneficiaryName == "" || utf8.RuneCountInString(in.BeneficiaryName) > 128:
		return invalid("beneficiary_name", "max_len", "beneficiary_name is required and at most 128 characters")
	case !validABARouting(in.RoutingNumber):
		return invalid("routing_number", "aba", "routing_number is not a valid ABA routing number")
	case len(in.AccountNumber) < 4 || len(in.AccountNumber) > 17 || !digitsOnly.MatchString(in.AccountNumber):
		return invalid("account_number", "digits_between", "account_number must have 4 to 17 digits")
	case in.AddressLine1 == "" || utf8.RuneCountInString(in.AddressLine1) > 128:
		return invalid("address_line_1", "max_len", "address_line_1 is required and at most 128 characters")
	case utf8.RuneCountInString(in.AddressLine2) > 128:
		return invalid("address_line_2", "max_len", "address_line_2 is at most 128 characters")
	case in.City == "" || utf8.RuneCountInString(in.City) > 64:
		return invalid("city", "max_len", "city is required and at most 64 characters")
	case !has(usStates, in.StateCode):
		return invalid("state_code", "in", "state_code must be a US state or DC")
	case !zipCode.MatchString(in.PostalCode):
		return invalid("postal_code", "zip", "postal_code must be NNNNN or NNNNN-NNNN")
	case in.AccountClass == AccountClassBusiness && !has(naicsCodes, in.BusinessIndustry):
		return invalid("business_industry", "in", "business_industry must be a NAICS code a US bank payout accepts")
	}
	return nil
}

func has(values map[string]struct{}, v string) bool {
	_, ok := values[v]
	return ok
}

func invalid(field, rule, message string) error {
	return &FieldError{Field: field, Rule: rule, Message: message}
}

// validABARouting checks the nine digits, the Federal Reserve prefix
// (01–12, 21–32, 61–72, 80) and the ABA checksum
// 3(d1+d4+d7) + 7(d2+d5+d8) + (d3+d6+d9) ≡ 0 (mod 10).
func validABARouting(s string) bool {
	if len(s) != 9 || !digitsOnly.MatchString(s) {
		return false
	}
	prefix := int(s[0]-'0')*10 + int(s[1]-'0')
	if !(prefix >= 1 && prefix <= 12 || prefix >= 21 && prefix <= 32 || prefix >= 61 && prefix <= 72 || prefix == 80) {
		return false
	}
	d := func(i int) int { return int(s[i] - '0') }
	sum := 3*(d(0)+d(3)+d(6)) + 7*(d(1)+d(4)+d(7)) + (d(2) + d(5) + d(8))
	return sum%10 == 0
}
