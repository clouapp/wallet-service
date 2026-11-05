// Package wiretime puts a timestamp in the one shape the client API sends:
// UTC at whole seconds, which encoding/json writes as "2006-01-02T15:04:05Z".
//
// Every timestamp column is timestamp(0), so a value read back from the
// database already has that shape. One that has not been read back does not:
// the request that completes an order answers completed_at from the
// time.Now() it just wrote, in the server's own zone with nanoseconds, and
// Android 13 and older cannot parse that offset. Resources and the SSE
// payloads pass their timestamps through here, so what a request answers is
// what a later read answers.
package wiretime

import "time"

// Of returns t in UTC, rounded to the second the way Postgres rounds a
// timestamp(0) when it stores it.
func Of(t time.Time) time.Time {
	return t.UTC().Round(time.Second)
}

// Ptr is Of for an optional timestamp; an absent one stays absent.
func Ptr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	wire := Of(*t)
	return &wire
}
