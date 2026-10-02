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

func newTestRevoker(t *testing.T, now time.Time) (*authsvc.SessionRevoker, *fakeWatermarks, *fakeRefreshRevoker) {
	t.Helper()
	watermarks := &fakeWatermarks{at: map[uuid.UUID]time.Time{}}
	refresh := &fakeRefreshRevoker{}
	revoker, err := authsvc.NewSessionRevoker(watermarks, refresh)
	require.NoError(t, err)
	return revoker.WithClock(func() time.Time { return now }), watermarks, refresh
}

func TestSessionRevoker_MovesTheWatermarkAndRevokesRefreshTokens(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 750_000_000, time.UTC)
	revoker, watermarks, refresh := newTestRevoker(t, now)
	userID := uuid.New()

	watermark, err := revoker.RevokeAll(userID)

	require.NoError(t, err)
	require.Equal(t, now.Truncate(time.Second), watermark, "second precision, like a JWT iat")
	require.Equal(t, watermark, watermarks.at[userID])
	require.Equal(t, []uuid.UUID{userID}, refresh.revoked)
}

func TestSessionRevoker_ReplacementSessionIssuedInTheSameSecondSurvives(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 750_000_000, time.UTC)
	revoker, _, _ := newTestRevoker(t, now)

	watermark, err := revoker.RevokeAll(uuid.New())
	require.NoError(t, err)

	replacementIssuedAt := now.Truncate(time.Second)
	require.False(t, authsvc.SessionRevoked(replacementIssuedAt, &watermark))
	require.True(t, authsvc.SessionRevoked(replacementIssuedAt.Add(-time.Second), &watermark))
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
