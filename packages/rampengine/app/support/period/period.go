// Package period parses the optional yyyy-MM-dd from/to query params into
// half-open created_at windows, and slices a window into evenly spaced buckets
// for a time series.
//
// Every parser reads a bound through parseDay and moves the upper one to the
// next midnight through dayAfter. What differs per caller is the timezone and
// what an unparseable bound means: the metrics endpoints (ParseYYYYMMDD) answer
// it with a FieldError, the listings (ParseClientDateRange in Sao Paulo time,
// ParseAdminDateRange in UTC) drop it, since the date_ymd rule has already
// refused it.
package period

import (
	"errors"
	"time"
)

var (
	// ErrInvalidDate reports a non-empty bound that is not a valid YYYY-MM-DD
	// calendar date. Match it with errors.Is; FieldError names which bound.
	ErrInvalidDate = errors.New("invalid date")
	// ErrInvertedRange reports a window whose lower bound is later than its
	// upper bound. Match it with errors.Is.
	ErrInvertedRange = errors.New("from after to")
)

// FieldError attributes a parse failure to the query param that caused it, so a
// controller can put the validation message under the right key instead of
// always blaming "from". Unwraps to one of the sentinels above.
type FieldError struct {
	Field string
	Err   error
}

// Error implements error.
func (e *FieldError) Error() string { return e.Field + ": " + e.Err.Error() }

// Unwrap exposes the sentinel so errors.Is keeps working on the wrapper.
func (e *FieldError) Unwrap() error { return e.Err }

// Period is a parsed date window.
//
// FromDate and ToDate are the calendar dates exactly as the caller asked for
// them, which is what the response echoes back. ToExclusive is ToDate advanced
// by one day: created_at filters compare against it with `<`, so the whole "to"
// day is inside the window. A nil bound means unbounded on that side.
type Period struct {
	FromDate    *time.Time
	ToDate      *time.Time
	ToExclusive *time.Time
}

// ParseYYYYMMDD turns the optional from/to strings into a half-open
// [FromDate, ToExclusive) window. An empty string leaves that bound unset, so
// two empty strings mean all-time.
//
// It returns a *FieldError wrapping ErrInvalidDate for a value that is not a
// valid YYYY-MM-DD date, naming the offending param, and one wrapping
// ErrInvertedRange when from is later than to. from equal to to is a valid
// single-day window.
func ParseYYYYMMDD(fromStr, toStr string) (Period, error) {
	var p Period
	from, err := parseDay(fromStr, time.UTC)
	if err != nil {
		return Period{}, &FieldError{Field: "from", Err: ErrInvalidDate}
	}
	to, err := parseDay(toStr, time.UTC)
	if err != nil {
		return Period{}, &FieldError{Field: "to", Err: ErrInvalidDate}
	}
	p.FromDate, p.ToDate, p.ToExclusive = from, to, dayAfter(to)
	if p.FromDate != nil && p.ToDate != nil && p.FromDate.After(*p.ToDate) {
		return Period{}, &FieldError{Field: "from", Err: ErrInvertedRange}
	}
	return p, nil
}

// dateLayout is the one date format every window is given in.
const dateLayout = "2006-01-02"

// parseDay reads a yyyy-MM-dd bound as midnight in loc. An empty bound is
// unset: nil, and no error.
func parseDay(value string, loc *time.Location) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	day, err := time.ParseInLocation(dateLayout, value, loc)
	if err != nil {
		return nil, err
	}
	return &day, nil
}

// dayAfter is the midnight that closes an upper bound's day, so the whole day
// is inside a window compared with `<`. It is a calendar step rather than 24
// hours: on a day a DST change made 23 or 25 hours long, those land an hour
// off. nil stays nil.
func dayAfter(day *time.Time) *time.Time {
	if day == nil {
		return nil
	}
	next := day.AddDate(0, 0, 1)
	return &next
}
