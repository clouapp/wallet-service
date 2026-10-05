package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

type revocationTx struct{}

type fakeWatermarks struct {
	at     map[uuid.UUID]time.Time
	err    error
	joined bool
}

func (f *fakeWatermarks) UpdateSessionsRevokedAt(ctx context.Context, id uuid.UUID, at time.Time) error {
	if ctx != nil && ctx.Value(revocationTx{}) != nil {
		f.joined = true
	}
	if f.err != nil {
		return f.err
	}
	f.at[id] = at
	return nil
}

type fakeRefreshRevoker struct {
	revoked []uuid.UUID
	err     error
	joined  bool
}

func (f *fakeRefreshRevoker) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	if ctx != nil && ctx.Value(revocationTx{}) != nil {
		f.joined = true
	}
	if f.err != nil {
		return f.err
	}
	f.revoked = append(f.revoked, userID)
	return nil
}

type recordingSessionActivity struct {
	rows   []models.AccountActivity
	fail   error
	opened bool
}

func (a *recordingSessionActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	a.opened = true
	return fn(context.WithValue(ctx, revocationTx{}, true))
}

func (a *recordingSessionActivity) Append(_ context.Context, row models.AccountActivity) error {
	if a.fail != nil {
		return a.fail
	}
	a.rows = append(a.rows, row)
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
	revoker, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{
		Watermarks: watermarks,
		Refresh:    refresh,
		Now:        func() time.Time { return now },
		Sleep:      sleeps.sleep,
	})
	require.NoError(t, err)
	return revoker, watermarks, refresh, sleeps
}

func TestSessionRevoker_MovesTheWatermarkAndRevokesRefreshTokens(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 750_000_000, time.UTC)
	revoker, watermarks, refresh := newTestRevoker(t, now)
	userID := uuid.New()

	watermark, err := revoker.RevokeAll(context.Background(), userID)

	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 10, 2, 12, 0, 6, 0, time.UTC), watermark, "start of the next second")
	require.Equal(t, watermark, watermarks.at[userID])
	require.Equal(t, []uuid.UUID{userID}, refresh.revoked)
}

func TestSessionRevoker_VoidsEverythingIssuedDuringTheRevocationSecond(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)
	revoker, _, _ := newTestRevoker(t, now)

	watermark, err := revoker.RevokeAll(context.Background(), uuid.New())
	require.NoError(t, err)

	require.True(t, authsvc.SessionRevoked(now, &watermark), "a token minted in the revocation second is identical to a fresh one, so it must die")
	require.True(t, authsvc.SessionRevoked(now.Add(-time.Second), &watermark))
	require.False(t, authsvc.SessionRevoked(now.Add(time.Second), &watermark))
}

func TestSessionRevoker_AwaitIssuableWaitsOutTheRevocationSecond(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 250_000_000, time.UTC)
	revoker, _, _, sleeps := newObservedRevoker(t, now)

	watermark, err := revoker.RevokeAll(context.Background(), uuid.New())
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
	revoker, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{
		Watermarks: &fakeWatermarks{err: watermarkFailure},
		Refresh:    &fakeRefreshRevoker{},
	})
	require.NoError(t, err)
	_, err = revoker.RevokeAll(context.Background(), uuid.New())
	require.ErrorIs(t, err, watermarkFailure)

	refreshFailure := errors.New("refresh down")
	revoker, err = authsvc.NewSessionRevoker(authsvc.RevokerDeps{
		Watermarks: &fakeWatermarks{at: map[uuid.UUID]time.Time{}},
		Refresh:    &fakeRefreshRevoker{err: refreshFailure},
	})
	require.NoError(t, err)
	_, err = revoker.RevokeAll(context.Background(), uuid.New())
	require.ErrorIs(t, err, refreshFailure)
}

func TestSessionRevoker_RejectsInvalidInput(t *testing.T) {
	_, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{Refresh: &fakeRefreshRevoker{}})
	require.Error(t, err)
	_, err = authsvc.NewSessionRevoker(authsvc.RevokerDeps{Watermarks: &fakeWatermarks{}})
	require.Error(t, err)

	revoker, _, _ := newTestRevoker(t, time.Now())
	_, err = revoker.RevokeAll(nil, uuid.New())
	require.Error(t, err)
	_, err = revoker.RevokeAll(context.Background(), uuid.Nil)
	require.Error(t, err)
}

func TestSessionRevoker_AttributesAPlatformRevokeToTheActor(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)
	watermarks := &fakeWatermarks{at: map[uuid.UUID]time.Time{}}
	refresh := &fakeRefreshRevoker{}
	activity := &recordingSessionActivity{}
	revoker, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{
		Watermarks: watermarks,
		Refresh:    refresh,
		Activity:   activity,
		Now:        func() time.Time { return now },
		Sleep:      func(time.Duration) {},
	})
	require.NoError(t, err)
	actorID := uuid.New()
	userID := uuid.New()

	_, err = revoker.RevokeAllBy(context.Background(), actorID, userID)

	require.NoError(t, err)
	require.Len(t, activity.rows, 1)
	require.Nil(t, activity.rows[0].AccountID)
	require.Equal(t, actorID, activity.rows[0].ActorUserID)
	require.Equal(t, userID.String(), activity.rows[0].TargetID)
	require.Equal(t, activitylog.ActionUserSessionsRevoked, activity.rows[0].Action)
	require.NotEqual(t, activitylog.ActionMemberSuspended, activity.rows[0].Action)

	_, err = revoker.RevokeAllBy(context.Background(), uuid.Nil, userID)
	require.Error(t, err)
}

func TestSessionRevoker_WritesAPlatformRowInsideTheTransaction(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)
	watermarks := &fakeWatermarks{at: map[uuid.UUID]time.Time{}}
	refresh := &fakeRefreshRevoker{}
	activity := &recordingSessionActivity{}
	revoker, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{
		Watermarks: watermarks,
		Refresh:    refresh,
		Activity:   activity,
		Now:        func() time.Time { return now },
		Sleep:      func(time.Duration) {},
	})
	require.NoError(t, err)
	userID := uuid.New()
	const tokenHash = "session-token-hash-must-not-be-stored"

	_, err = revoker.RevokeAll(context.Background(), userID)

	require.NoError(t, err)
	require.True(t, activity.opened)
	require.True(t, watermarks.joined)
	require.True(t, refresh.joined)
	require.Len(t, activity.rows, 1)
	row := activity.rows[0]
	require.Nil(t, row.AccountID)
	require.Equal(t, userID, row.ActorUserID)
	require.Equal(t, activitylog.ActionUserSessionsRevoked, row.Action)
	require.Equal(t, activitylog.TargetUser, row.TargetType)
	require.Equal(t, userID.String(), row.TargetID)
	encoded, err := row.Metadata.Encode()
	require.NoError(t, err)
	require.Contains(t, encoded, `"key":"sessions"`)
	require.NotContains(t, encoded, tokenHash)
	require.False(t, strings.Contains(encoded, "token_hash"))
	require.False(t, strings.Contains(encoded, "spending_limit"))
	require.False(t, strings.Contains(encoded, "-1"))
}

func TestSessionRevoker_ActivityFailureFailsTheRevocation(t *testing.T) {
	watermarks := &fakeWatermarks{at: map[uuid.UUID]time.Time{}}
	refresh := &fakeRefreshRevoker{}
	activity := &recordingSessionActivity{fail: errors.New("activity refused")}
	revoker, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{
		Watermarks: watermarks,
		Refresh:    refresh,
		Activity:   activity,
	})
	require.NoError(t, err)

	_, err = revoker.RevokeAll(context.Background(), uuid.New())

	require.Error(t, err)
	require.Empty(t, activity.rows)
}

func TestSessionRevoked(t *testing.T) {
	watermark := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)

	require.False(t, authsvc.SessionRevoked(watermark.Add(-time.Hour), nil), "no watermark, nothing revoked")
	require.True(t, authsvc.SessionRevoked(watermark.Add(-time.Second), &watermark))
	require.False(t, authsvc.SessionRevoked(watermark, &watermark))
	require.False(t, authsvc.SessionRevoked(watermark.Add(time.Second), &watermark))
	require.True(t, authsvc.SessionRevoked(time.Time{}, &watermark), "a token without iat is refused once a watermark exists")
}
