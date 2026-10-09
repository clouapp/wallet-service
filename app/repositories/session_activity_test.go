package repositories_test

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
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/stretchr/testify/assert"
)

func TestSession_Revocation_AndActivityCommitTogether(t *testing.T) {
	fixtures.TestDB(t)
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
	assert.False(t, watermark.IsZero())

	assert.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM users WHERE id = ? AND sessions_revoked_at IS NOT NULL`, userID))
	assert.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM refresh_tokens WHERE id = ? AND revoked_at IS NOT NULL`, tokenID))

	rows, total, err := activity.ListPlatform(context.Background(), 20, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, activitylog.ActionUserSessionsRevoked, rows[0].Action)
	assert.Nil(t, rows[0].AccountID)
	encoded, err := rows[0].Metadata.Encode()
	require.NoError(t, err)
	assert.NotContains(t, encoded, "refresh-hash-must-not-be-stored")

	listed, listedTotal, err := activity.List(context.Background(), uuid.New(), 20, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), listedTotal)
	assert.Empty(t, listed)
}

func TestSession_Revocation_RollsBackWhenActivityRefusesTheRow(t *testing.T) {
	fixtures.TestDB(t)
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
	assert.Error(t, err)

	assert.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM users WHERE id = ? AND sessions_revoked_at IS NULL`, userID))
	assert.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM refresh_tokens WHERE id = ? AND revoked_at IS NULL`, tokenID))
	assert.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM account_activity WHERE actor_user_id = ?`, userID))
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
	now := time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)
	revoker, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{
		Watermarks: repositories.NewUserRepository(nil),
		Refresh:    repositories.NewRefreshTokenRepository(nil),
		Activity:   activity,
		Now:        func() time.Time { return now },
		Sleep:      func(time.Duration) {},
	})
	require.NoError(t, err)
	return revoker
}

func insertSessionUser(t *testing.T) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		VALUES (?, ?, 'hash', 'active', NOW(), NOW())`, userID, userID.String()+"@example.com")
	return userID
}
