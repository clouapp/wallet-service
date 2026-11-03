package period

import (
	"time"
)

var clientLocation = func() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// ParseClientDateRange turns yyyy-MM-dd query params into a created_at window
// in the user's civil timezone (BRT). The upper bound is exclusive at the
// start of the day after date_to.
//
// Callers pass values already validated by the date_ymd rule; an empty bound,
// or one that still fails to parse, is left open rather than guessed.
func ParseClientDateRange(from, to string) (fromTime, toExclusive *time.Time) {
	if day, err := parseDay(from, clientLocation); err == nil {
		fromTime = day
	}
	if day, err := parseDay(to, clientLocation); err == nil {
		toExclusive = dayAfter(day)
	}
	return fromTime, toExclusive
}

// ParseAdminDateRange turns optional from/to query strings (yyyy-MM-dd) into a
// half-open [from, to) window in UTC, the timezone the admin explorers report
// in. `to` is advanced by one day so the whole "to" date is included.
//
// Callers pass values already validated by the date_ymd rule; a bound that
// still fails to parse is dropped rather than guessed, and an unparseable
// `from` drops the window.
func ParseAdminDateRange(fromStr, toStr string) (from, to *time.Time) {
	from, err := parseDay(fromStr, time.UTC)
	if err != nil {
		return nil, nil
	}
	day, err := parseDay(toStr, time.UTC)
	if err != nil {
		return from, nil
	}
	return from, dayAfter(day)
}
