package identity

import "time"

// RevocationStamp is the sessions_revoked_at value for a revocation at now, in
// whole UTC seconds. The column is a timestamp(0) without time zone: it would
// round a fraction up, refusing the next second's logins too, and store a
// non-UTC time's wall clock as if it were UTC.
func RevocationStamp(now time.Time) time.Time {
	return now.UTC().Truncate(time.Second)
}

// AcceptsTokenIssuedAt reports whether a token issued at iat still speaks for
// a subject whose sessions were revoked at revokedAt (nil: never). Only a token
// issued in a second after the stamp's does. A token's iat has one-second
// precision, so one minted in the stamp's own second may predate the
// revocation, for example a stolen token refreshed at the start of the second
// the owner reset the password. It is refused with the rest. Nothing
// legitimate mints a token that fast: a reset returns none, and a login needs
// the code it mails.
func AcceptsTokenIssuedAt(revokedAt *time.Time, iat time.Time) bool {
	return revokedAt == nil || iat.Truncate(time.Second).After(revokedAt.Truncate(time.Second))
}
