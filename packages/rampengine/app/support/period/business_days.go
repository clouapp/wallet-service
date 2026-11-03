package period

import "time"

// isBusinessDay: weekends are the only days that do not count. Holidays are
// deliberately not modelled.
func isBusinessDay(t time.Time) bool {
	switch t.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	return true
}

// AddBusinessDays moves t by n business days (negative goes back), keeping the
// time of day. Saturday and Sunday never count, so Friday plus one is Monday
// and Monday minus one is Friday. Zero returns t unchanged.
func AddBusinessDays(t time.Time, n int) time.Time {
	step := 1
	if n < 0 {
		step, n = -1, -n
	}
	for n > 0 {
		t = t.AddDate(0, 0, step)
		if isBusinessDay(t) {
			n--
		}
	}
	return t
}

// BusinessDaysBetween counts the business days after from's calendar day up to
// and including to's, the inverse of AddBusinessDays: it is negative when to is
// before from. Both dates are read in their own location.
func BusinessDaysBetween(from, to time.Time) int {
	if to.Before(from) {
		return -BusinessDaysBetween(to, from)
	}
	n := 0
	fy, fm, fd := from.Date()
	cur := time.Date(fy, fm, fd, 0, 0, 0, 0, from.Location())
	ty, tm, td := to.Date()
	end := time.Date(ty, tm, td, 0, 0, 0, 0, to.Location())
	for cur = cur.AddDate(0, 0, 1); !cur.After(end); cur = cur.AddDate(0, 0, 1) {
		if isBusinessDay(cur) {
			n++
		}
	}
	return n
}
