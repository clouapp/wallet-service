package kyc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Bridge replays an idempotency key for 24 hours and then refuses it for good
// with 422 "Idempotency Key Retry Deadline Exceeded". A key fixed per user and
// provider therefore locked the terms step forever for anyone who came back
// more than a day after first opening it — user 2 in production, 24/09/2026.

func TestDerivedTosKeyIsStableWithinAUTCDay(t *testing.T) {
	morning := time.Date(2026, 9, 24, 0, 0, 1, 0, time.UTC)
	night := time.Date(2026, 9, 24, 23, 59, 59, 0, time.UTC)

	require.Equal(t,
		derivedTosKey(2, "bridge", "xip://kyc/tos", morning),
		derivedTosKey(2, "bridge", "xip://kyc/tos", night))
}

func TestDerivedTosKeyChangesOnTheNextUTCDay(t *testing.T) {
	today := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	tomorrow := today.Add(24 * time.Hour)

	require.NotEqual(t,
		derivedTosKey(2, "bridge", "xip://kyc/tos", today),
		derivedTosKey(2, "bridge", "xip://kyc/tos", tomorrow))
}

func TestDerivedTosKeyBucketsByUTCNotLocalTime(t *testing.T) {
	brt := time.FixedZone("BRT", -3*60*60)
	lateEveningInBrazil := time.Date(2026, 9, 24, 22, 30, 0, 0, brt) // 01:30 UTC on the 25th
	nextUTCDay := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)

	require.Equal(t,
		derivedTosKey(2, "bridge", "", nextUTCDay),
		derivedTosKey(2, "bridge", "", lateEveningInBrazil))
}
