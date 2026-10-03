package auth_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authsvc "github.com/macrowallets/waas/app/services/auth"
)

type fakeWatermarks struct {
	at  map[uuid.UUID]time.Time
	err error
}

func (f *fakeWatermarks) UpdateSessionsRevokedAt(id uuid.UUID, at time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.at[id] = at
	return nil
}

type fakeRefreshRevoker struct {
	revoked []uuid.UUID
	err     error
}

func (f *fakeRefreshRevoker) RevokeAllForUser(userID uuid.UUID) error {
	if f.err != nil {
		return f.err
	}
	f.revoked = append(f.revoked, userID)
	return nil
}

type recordedSleeps struct {
	waits []time.Duration
}

func (r *recordedSleeps) sleep(d time.Duration) { r.waits = append(r.waits, d) }

func newTestRevoker(t *testing.T, now time.Time) (*authsvc.SessionRevoker, *fakeWatermarks, *fakeRefreshRevoker) {
	t.Helper()
	revoker, watermarks, refresh, _ := newObservedRevoker(t, now)
	return revoker, watermarks, refresh
}

func newObservedRevoker(t *testing.T, now time.Time) (*authsvc.SessionRevoker, *fakeWatermarks, *fakeRefreshRevoker, *recordedSleeps) {
	t.Helper()
	watermarks := &fakeWatermarks{at: map[uuid.UUID]time.Time{}}
	refresh := &fakeRefreshRevoker{}
	sleeps := &recordedSleeps{}
	revoker, err := authsvc.NewSessionRevoker(watermarks, refresh)
	require.NoError(t, err)
	return revoker.WithClock(func() time.Time { return now }, sleeps.sleep), watermarks, refresh, sleeps
}

func TestSessionRevoker_MovesTheWatermarkAndRevokesRefreshTokens(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 750_000_000, time.UTC)
	revoker, watermarks, refresh := newTestRevoker(t, now)
	userID := uuid.New()

	watermark, err := revoker.RevokeAll(userID)

	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 10, 2, 12, 0, 6, 0, time.UTC), watermark, "start of the next second")
	require.Equal(t, watermark, watermarks.at[userID])
	require.Equal(t, []uuid.UUID{userID}, refresh.revoked)
}

func TestSessionRevoker_VoidsEverythingIssuedDuringTheRevocationSecond(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)
	revoker, _, _ := newTestRevoker(t, now)

	watermark, err := revoker.RevokeAll(uuid.New())
	require.NoError(t, err)

	require.True(t, authsvc.SessionRevoked(now, &watermark), "a token minted in the revocation second is identical to a fresh one, so it must die")
	require.True(t, authsvc.SessionRevoked(now.Add(-time.Second), &watermark))
	require.False(t, authsvc.SessionRevoked(now.Add(time.Second), &watermark))
}

func TestSessionRevoker_AwaitIssuableWaitsOutTheRevocationSecond(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 250_000_000, time.UTC)
	revoker, _, _, sleeps := newObservedRevoker(t, now)

	watermark, err := revoker.RevokeAll(uuid.New())
	require.NoError(t, err)
	require.NoError(t, revoker.AwaitIssuable(&watermark))

	require.Equal(t, []time.Duration{750 * time.Millisecond}, sleeps.waits)
}

func TestSessionRevoker_AwaitIssuableDoesNotWaitOnceTheWatermarkPassed(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)
	revoker, _, _, sleeps := newObservedRevoker(t, now)

	require.NoError(t, revoker.AwaitIssuable(nil))
	passed := now.Add(-time.Minute)
	require.NoError(t, revoker.AwaitIssuable(&passed))
	exactlyNow := now
	require.NoError(t, revoker.AwaitIssuable(&exactlyNow))

	require.Empty(t, sleeps.waits)
}

func TestSessionRevoker_AwaitIssuableRefusesAWatermarkFarAhead(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)
	revoker, _, _, sleeps := newObservedRevoker(t, now)

	farAhead := now.Add(time.Minute)
	require.Error(t, revoker.AwaitIssuable(&farAhead))
	require.Empty(t, sleeps.waits, "a skewed clock must not stall the request")
}

func TestSessionRevoker_PropagatesStoreFailures(t *testing.T) {
	watermarkFailure := errors.New("db down")
	revoker, err := authsvc.NewSessionRevoker(&fakeWatermarks{err: watermarkFailure}, &fakeRefreshRevoker{})
	require.NoError(t, err)
	_, err = revoker.RevokeAll(uuid.New())
	require.ErrorIs(t, err, watermarkFailure)

	refreshFailure := errors.New("refresh down")
	revoker, err = authsvc.NewSessionRevoker(&fakeWatermarks{at: map[uuid.UUID]time.Time{}}, &fakeRefreshRevoker{err: refreshFailure})
	require.NoError(t, err)
	_, err = revoker.RevokeAll(uuid.New())
	require.ErrorIs(t, err, refreshFailure)
}

func TestSessionRevoker_RejectsInvalidInput(t *testing.T) {
	_, err := authsvc.NewSessionRevoker(nil, &fakeRefreshRevoker{})
	require.Error(t, err)
	_, err = authsvc.NewSessionRevoker(&fakeWatermarks{}, nil)
	require.Error(t, err)

	revoker, _, _ := newTestRevoker(t, time.Now())
	_, err = revoker.RevokeAll(uuid.Nil)
	require.Error(t, err)
}

func TestSessionRevoked(t *testing.T) {
	watermark := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)

	require.False(t, authsvc.SessionRevoked(watermark.Add(-time.Hour), nil), "no watermark, nothing revoked")
	require.True(t, authsvc.SessionRevoked(watermark.Add(-time.Second), &watermark))
	require.False(t, authsvc.SessionRevoked(watermark, &watermark))
	require.False(t, authsvc.SessionRevoked(watermark.Add(time.Second), &watermark))
	require.True(t, authsvc.SessionRevoked(time.Time{}, &watermark), "a token without iat is refused once a watermark exists")
}
