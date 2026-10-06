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
	"github.com/stretchr/testify/assert"
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

func TestSession_Revoker_MovesTheWatermarkAndRevokesRefreshTokens(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 750_000_000, time.UTC)
	revoker, watermarks, refresh := newTestRevoker(t, now)
	userID := uuid.New()

	watermark, err := revoker.RevokeAll(context.Background(), userID)

	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 2, 12, 0, 6, 0, time.UTC), watermark, "start of the next second")
	assert.Equal(t, watermark, watermarks.at[userID])
	assert.Equal(t, []uuid.UUID{userID}, refresh.revoked)
}

func TestSession_Revoker_VoidsEverythingIssuedDuringTheRevocationSecond(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)
	revoker, _, _ := newTestRevoker(t, now)

	watermark, err := revoker.RevokeAll(context.Background(), uuid.New())
	require.NoError(t, err)

	assert.True(t, authsvc.SessionRevoked(now, &watermark), "a token minted in the revocation second is identical to a fresh one, so it must die")
	assert.True(t, authsvc.SessionRevoked(now.Add(-time.Second), &watermark))
	assert.False(t, authsvc.SessionRevoked(now.Add(time.Second), &watermark))
}

func TestSession_Revoker_AwaitIssuableWaitsOutTheRevocationSecond(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 250_000_000, time.UTC)
	revoker, _, _, sleeps := newObservedRevoker(t, now)

	watermark, err := revoker.RevokeAll(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NoError(t, revoker.AwaitIssuable(&watermark))

	assert.Equal(t, []time.Duration{750 * time.Millisecond}, sleeps.waits)
}

func TestSession_Revoker_AwaitIssuableDoesNotWaitOnceTheWatermarkPassed(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)
	revoker, _, _, sleeps := newObservedRevoker(t, now)

	require.NoError(t, revoker.AwaitIssuable(nil))
	passed := now.Add(-time.Minute)
	require.NoError(t, revoker.AwaitIssuable(&passed))
	exactlyNow := now
	require.NoError(t, revoker.AwaitIssuable(&exactlyNow))

	assert.Empty(t, sleeps.waits)
}

func TestSession_Revoker_AwaitIssuableRefusesAWatermarkFarAhead(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)
	revoker, _, _, sleeps := newObservedRevoker(t, now)

	farAhead := now.Add(time.Minute)
	assert.Error(t, revoker.AwaitIssuable(&farAhead))
	assert.Empty(t, sleeps.waits, "a skewed clock must not stall the request")
}

func TestSession_Revoker_PropagatesStoreFailures(t *testing.T) {
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
	assert.ErrorIs(t, err, refreshFailure)
}

func TestSession_Revoker_RejectsInvalidInput(t *testing.T) {
	_, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{Refresh: &fakeRefreshRevoker{}})
	assert.Error(t, err)
	_, err = authsvc.NewSessionRevoker(authsvc.RevokerDeps{Watermarks: &fakeWatermarks{}})
	assert.Error(t, err)

	revoker, _, _ := newTestRevoker(t, time.Now())
	_, err = revoker.RevokeAll(nil, uuid.New())
	assert.Error(t, err)
	_, err = revoker.RevokeAll(context.Background(), uuid.Nil)
	assert.Error(t, err)
}

func TestSession_Revoker_AttributesAPlatformRevokeToTheActor(t *testing.T) {
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
	assert.Nil(t, activity.rows[0].AccountID)
	assert.Equal(t, actorID, activity.rows[0].ActorUserID)
	assert.Equal(t, userID.String(), activity.rows[0].TargetID)
	assert.Equal(t, activitylog.ActionUserSessionsRevoked, activity.rows[0].Action)
	assert.NotEqual(t, activitylog.ActionMemberSuspended, activity.rows[0].Action)

	_, err = revoker.RevokeAllBy(context.Background(), uuid.Nil, userID)
	assert.Error(t, err)
}

func TestSession_Revoker_WritesAPlatformRowInsideTheTransaction(t *testing.T) {
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
	assert.True(t, activity.opened)
	assert.True(t, watermarks.joined)
	assert.True(t, refresh.joined)
	require.Len(t, activity.rows, 1)
	row := activity.rows[0]
	assert.Nil(t, row.AccountID)
	assert.Equal(t, userID, row.ActorUserID)
	assert.Equal(t, activitylog.ActionUserSessionsRevoked, row.Action)
	assert.Equal(t, activitylog.TargetUser, row.TargetType)
	assert.Equal(t, userID.String(), row.TargetID)
	encoded, err := row.Metadata.Encode()
	require.NoError(t, err)
	assert.Contains(t, encoded, `"key":"sessions"`)
	assert.NotContains(t, encoded, tokenHash)
	assert.False(t, strings.Contains(encoded, "token_hash"))
	assert.False(t, strings.Contains(encoded, "spending_limit"))
	assert.False(t, strings.Contains(encoded, "-1"))
}

func TestSession_Revoker_ActivityFailureFailsTheRevocation(t *testing.T) {
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

	assert.Error(t, err)
	assert.Empty(t, activity.rows)
}

func TestSessionRevocation_Session_Revoked(t *testing.T) {
	watermark := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)

	assert.False(t, authsvc.SessionRevoked(watermark.Add(-time.Hour), nil), "no watermark, nothing revoked")
	assert.True(t, authsvc.SessionRevoked(watermark.Add(-time.Second), &watermark))
	assert.False(t, authsvc.SessionRevoked(watermark, &watermark))
	assert.False(t, authsvc.SessionRevoked(watermark.Add(time.Second), &watermark))
	assert.True(t, authsvc.SessionRevoked(time.Time{}, &watermark), "a token without iat is refused once a watermark exists")
}
