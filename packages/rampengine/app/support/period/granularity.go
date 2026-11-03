package period

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidGranularity reports a bucket size outside the supported
	// vocabulary. Match it with errors.Is.
	ErrInvalidGranularity = errors.New("invalid granularity")
	// ErrUnboundedPeriod reports bucket math asked of a window that is open on
	// at least one side. A series needs a first and a last bucket to zero-fill
	// between; "all-time" has to be resolved to real dates before it gets here.
	ErrUnboundedPeriod = errors.New("period is unbounded")
	// ErrTooManyBuckets reports a window that does not fit the caller's point
	// budget at any supported granularity.
	ErrTooManyBuckets = errors.New("too many buckets")
)

// Granularity is the bucket size a time series is grouped into.
type Granularity string

// The supported bucket sizes.
const (
	GranularityDay   Granularity = "day"
	GranularityWeek  Granularity = "week"
	GranularityMonth Granularity = "month"
)

// AllGranularities lists the bucket sizes from finest to coarsest. FitGranularity
// walks it in this order, so it doubles as the auto-selection preference.
var AllGranularities = []Granularity{GranularityDay, GranularityWeek, GranularityMonth}

// ParseGranularity turns a query-string value into a Granularity. The empty
// string is rejected like any other unknown value: "unset" means "let the
// server choose", which is the caller's decision to make, not this function's.
func ParseGranularity(s string) (Granularity, error) {
	for _, g := range AllGranularities {
		if string(g) == s {
			return g, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidGranularity, s)
}

// Truncate returns the start of the bucket t falls in, as midnight UTC.
//
// Everything happens in UTC because that is the calendar ParseYYYYMMDD already
// windows on: a bucket boundary in any other zone would put transactions on a
// different day than the from/to filter that selected them, and the series
// would stop adding up to the overview for the same window.
//
// The week starts on Monday, matching ISO-8601 and Postgres' date_trunc.
func (g Granularity) Truncate(t time.Time) time.Time {
	utc := t.UTC()
	day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)

	switch g {
	case GranularityWeek:
		// Go counts Sunday as 0; shifting by 6 makes Monday the zero offset.
		offset := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -offset)
	case GranularityMonth:
		return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return day
	}
}

// Next returns the start of the bucket that follows the one starting at t.
func (g Granularity) Next(t time.Time) time.Time {
	switch g {
	case GranularityWeek:
		return t.AddDate(0, 0, 7)
	case GranularityMonth:
		return t.AddDate(0, 1, 0)
	default:
		return t.AddDate(0, 0, 1)
	}
}

// BucketCount reports how many buckets of size g cover p, including the
// partial ones at either end. It is arithmetic rather than iteration so a
// caller can reject an oversized window before allocating for it.
func BucketCount(p Period, g Granularity) (int, error) {
	first, last, err := g.ends(p)
	if err != nil {
		return 0, err
	}

	switch g {
	case GranularityMonth:
		months := (last.Year()-first.Year())*12 + int(last.Month()) - int(first.Month())
		return months + 1, nil
	case GranularityWeek:
		// Both ends are Mondays, so the gap is always a whole number of weeks.
		return int(last.Sub(first)/(7*24*time.Hour)) + 1, nil
	default:
		// Both ends are UTC midnights, so no DST-shortened day can round this.
		return int(last.Sub(first)/(24*time.Hour)) + 1, nil
	}
}

// Buckets returns every bucket start of size g covering p, in order. This is
// the skeleton a series is zero-filled onto: a day with no movement still gets
// a point, because a gap in the x-axis reads as continuity rather than as the
// absence it is.
func Buckets(p Period, g Granularity) ([]time.Time, error) {
	first, last, err := g.ends(p)
	if err != nil {
		return nil, err
	}

	count, err := BucketCount(p, g)
	if err != nil {
		return nil, err
	}

	out := make([]time.Time, 0, count)
	for cursor := first; !cursor.After(last); cursor = g.Next(cursor) {
		out = append(out, cursor)
	}
	return out, nil
}

// FitGranularity picks the finest bucket size whose series fits in max points.
// It is what an omitted `granularity` resolves to: a ten-year window plotted
// daily is 3650 points nobody can read and nobody asked for, and refusing it
// outright would make "all time" unusable.
func FitGranularity(p Period, max int) (Granularity, error) {
	for _, g := range AllGranularities {
		count, err := BucketCount(p, g)
		if err != nil {
			return "", err
		}
		if count <= max {
			return g, nil
		}
	}
	return "", fmt.Errorf("%w: no granularity fits %d points", ErrTooManyBuckets, max)
}

// ends returns the first and last bucket start covering p. Both bounds must be
// set: ToDate is the inclusive calendar date the caller asked for, so the last
// bucket is the one that day falls in.
func (g Granularity) ends(p Period) (first, last time.Time, err error) {
	switch {
	case p.FromDate == nil && p.ToDate == nil:
		return time.Time{}, time.Time{}, fmt.Errorf("%w: neither bound is set", ErrUnboundedPeriod)
	case p.FromDate == nil:
		return time.Time{}, time.Time{}, fmt.Errorf("%w: from is not set", ErrUnboundedPeriod)
	case p.ToDate == nil:
		return time.Time{}, time.Time{}, fmt.Errorf("%w: to is not set", ErrUnboundedPeriod)
	}
	return g.Truncate(*p.FromDate), g.Truncate(*p.ToDate), nil
}
