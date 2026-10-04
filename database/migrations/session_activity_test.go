package migrations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestSessionRevocationAndActivityCommitTogether(t *testing.T) {
	mocks.TestDB(t)
	userID := insertSessionUser(t)
	refresh := repositories.NewRefreshTokenRepository(nil)
	tokenID := uuid.New()
	require.NoError(t, refresh.Create(context.Background(), &models.RefreshToken{
		ID:        tokenID,
		UserID:    userID,
		TokenHash: "refresh-hash-must-not-be-stored",
		ExpiresAt: time.Now().Add(time.Hour),
	}))
	activity := repositories.NewAccountActivityRepository(nil)
	revoker := sessionRevoker(t, activity)

	watermark, err := revoker.RevokeAll(context.Background(), userID)
	require.NoError(t, err)
	require.False(t, watermark.IsZero())

	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM users WHERE id = ? AND sessions_revoked_at IS NOT NULL`, userID))
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM refresh_tokens WHERE id = ? AND revoked_at IS NOT NULL`, tokenID))

	rows, total, err := activity.ListPlatform(context.Background(), 20, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, activitylog.ActionUserSessionsRevoked, rows[0].Action)
	require.Nil(t, rows[0].AccountID)
	encoded, err := rows[0].Metadata.Encode()
	require.NoError(t, err)
	require.NotContains(t, encoded, "refresh-hash-must-not-be-stored")

	listed, listedTotal, err := activity.List(context.Background(), uuid.New(), 20, 0)
	require.NoError(t, err)
	require.Equal(t, int64(0), listedTotal)
	require.Empty(t, listed)
}

func TestSessionRevocationRollsBackWhenActivityRefusesTheRow(t *testing.T) {
	mocks.TestDB(t)
	userID := insertSessionUser(t)
	refresh := repositories.NewRefreshTokenRepository(nil)
	tokenID := uuid.New()
	require.NoError(t, refresh.Create(context.Background(), &models.RefreshToken{
		ID:        tokenID,
		UserID:    userID,
		TokenHash: "refresh-hash-rolls-back",
		ExpiresAt: time.Now().Add(time.Hour),
	}))
	revoker := sessionRevoker(t, refuseSessionActivity{inner: repositories.NewAccountActivityRepository(nil)})

	_, err := revoker.RevokeAll(context.Background(), userID)
	require.Error(t, err)

	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM users WHERE id = ? AND sessions_revoked_at IS NULL`, userID))
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM refresh_tokens WHERE id = ? AND revoked_at IS NULL`, tokenID))
	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM account_activity WHERE actor_user_id = ?`, userID))
}

type refuseSessionActivity struct {
	inner *repositories.AccountActivityRepository
}

func (r refuseSessionActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	return r.inner.Within(ctx, fn)
}

func (r refuseSessionActivity) Append(context.Context, models.AccountActivity) error {
	return errors.New("activity refused")
}

func sessionRevoker(t *testing.T, activity authsvc.SessionActivity) *authsvc.SessionRevoker {
	t.Helper()
	revoker, err := authsvc.NewSessionRevoker(
		repositories.NewUserRepository(nil),
		repositories.NewRefreshTokenRepository(nil),
	)
	require.NoError(t, err)
	now := time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)
	return revoker.WithClock(func() time.Time { return now }, func(time.Duration) {}).WithActivity(activity)
}

func insertSessionUser(t *testing.T) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		VALUES (?, ?, 'hash', 'active', NOW(), NOW())`, userID, userID.String()+"@example.com")
	return userID
}
