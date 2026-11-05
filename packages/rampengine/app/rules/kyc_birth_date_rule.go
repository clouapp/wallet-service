package rules

import (
	"context"
	"time"

	"github.com/goravel/framework/contracts/validation"
)

const (
	// birthDateLayout is the storage format the whole KYC contract speaks:
	// the client posts it, the adapters forward it, and both providers want
	// it. time.Parse with this layout is strict about the calendar, which is
	// the half `regex:^\d{4}-\d{2}-\d{2}$` never did — it admitted month 13,
	// day 45 and 29 February in a common year.
	birthDateLayout = "2006-01-02"
	// minimumKycAge is the age a person must have reached to hold a XIP
	// account. It is refused here rather than upstream so the user is told
	// which field is the problem: a provider's rejection arrives as a
	// compliance failure with no field named, and reads to the app as an
	// outage.
	minimumKycAge = 18
	// earliestBirthYear bounds the other end. Nobody transacting is older
	// than this, so a date behind it is a typo or a broken client, and
	// letting it through only moves the refusal to a provider that will
	// word it worse.
	earliestBirthYear = 1900
)

// KycBirthDateRule accepts a real calendar date, in the past, belonging to
// someone old enough to hold an account.
//
// The date is the one field on the KYC form whose validity this side can
// decide entirely on its own — no provider call, no country table, no
// canonicalisation. Deferring it bought nothing and cost the user a specific
// message: measured 01/09, 2030-01-01, 2020-01-01, 1800-01-01 and 1990-13-45
// were all accepted here and fanned out to both providers.
type KycBirthDateRule struct{}

func (r *KycBirthDateRule) Signature() string { return "kyc_birth_date" }

func (r *KycBirthDateRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	// Parsed in UTC, and compared against a UTC today: the alternative is a
	// birthday that turns eighteen at a different instant depending on where
	// the server happens to run.
	birth, err := time.ParseInLocation(birthDateLayout, s, time.UTC)
	if err != nil {
		return false
	}
	// Parse accepts "1990-4-2" for this layout by padding it; the contract
	// stores a fixed width, so the round trip is what enforces it.
	if birth.Format(birthDateLayout) != s {
		return false
	}
	if birth.Year() < earliestBirthYear {
		return false
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	if !birth.Before(today) {
		return false
	}
	// AddDate on the birth date rather than arithmetic on the difference:
	// it is what makes 29 February behave, and it makes the boundary read
	// the way the rule is stated — the day they turn eighteen counts.
	return !birth.AddDate(minimumKycAge, 0, 0).After(today)
}

func (r *KycBirthDateRule) Message(_ context.Context) string {
	return "must be a real date in YYYY-MM-DD form, and the account holder must be at least 18"
}
