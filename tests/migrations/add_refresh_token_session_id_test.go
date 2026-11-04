package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func refreshTokenSessionIDColumn(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'refresh_tokens' AND column_name = 'session_id'
		  AND data_type = 'uuid' AND is_nullable = 'YES'`)
}

func refreshTokenSessionIDIndex(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM pg_indexes
		WHERE tablename = 'refresh_tokens' AND indexdef LIKE '%(session_id)%'`)
}

func TestAdd_RefreshTokenSessionID_AddsANullableIndexedColumnAndDropsIt(t *testing.T) {
	fixtures.TestDBFreshSchema(t)
	migration := &migrations.M00000000000720AddRefreshTokenSessionID{}
	assert.Equal(t, int64(1), refreshTokenSessionIDColumn(t))
	assert.Equal(t, int64(1), refreshTokenSessionIDIndex(t))

	userID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, full_name, status, created_at, updated_at)
		VALUES (?, 'session-id@example.com', 'hash', 'Ada', 'active', NOW(), NOW())`, userID)
	exec(t, `INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at, updated_at)
		VALUES (?, ?, 'legacy', NOW() + INTERVAL '1 day', NOW(), NOW())`, uuid.New(), userID)
	sessionID := uuid.New()
	exec(t, `INSERT INTO refresh_tokens (id, user_id, session_id, token_hash, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, 'current', NOW() + INTERVAL '1 day', NOW(), NOW())`, uuid.New(), userID, sessionID)
	assert.Equal(t, int64(1), countRows(t, `SELECT count(*) FROM refresh_tokens WHERE session_id IS NULL`), "a row from before keeps no session id")
	assert.Equal(t, int64(1), countRows(t, `SELECT count(*) FROM refresh_tokens WHERE session_id = ?`, sessionID))

	require.NoError(t, migration.Down())
	assert.Zero(t, refreshTokenSessionIDColumn(t))
	assert.Zero(t, refreshTokenSessionIDIndex(t))
	require.NoError(t, migration.Down(), "Down must be repeatable")
	assert.Equal(t, int64(2), countRows(t, `SELECT count(*) FROM refresh_tokens`), "the rows stay")

	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(1), refreshTokenSessionIDColumn(t))
	assert.Equal(t, int64(1), refreshTokenSessionIDIndex(t))
}
