package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestDropLegacyTotpSecretRemovesEmptyStores(t *testing.T) {
	fixtures.TestDB(t)

	require.False(t, legacyTotpSecretColumnPresent(t))
	require.False(t, legacyRecoveryTablePresent(t))

	migration := &migrations.M00000000000550DropLegacyTotpSecret{}
	require.NoError(t, migration.Down())
	require.True(t, legacyTotpSecretColumnPresent(t))
	require.True(t, legacyRecoveryTablePresent(t))
	require.Zero(t, countRows(t, `SELECT count(*) FROM totp_recovery_codes`))

	userID := uuid.New()
	_, err := facades.Orm().Query().Exec(`
		INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		VALUES (?, 'drop-totp@example.com', 'hash', 'active', NOW(), NOW())`, userID)
	require.NoError(t, err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, last_used_counter, created_at, updated_at
		) VALUES (?, 'users', ?, 'enc:v1:sealed-marker', 4, NOW(), NOW())`,
		uuid.New(), userID)
	require.NoError(t, err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_backup_codes (
			id, subject_type, subject_id, code_hash, created_at, updated_at
		) VALUES (?, 'users', ?, 'backup-hash-marker', NOW(), NOW())`,
		uuid.New(), userID)
	require.NoError(t, err)

	require.NoError(t, migration.Down())
	require.Empty(t, textColumn(t, `SELECT COALESCE(totp_secret, '') FROM users WHERE id = ?`, userID))
	require.Zero(t, countRows(t, `SELECT count(*) FROM totp_recovery_codes`))

	require.NoError(t, migration.Up())
	require.False(t, legacyTotpSecretColumnPresent(t))
	require.False(t, legacyRecoveryTablePresent(t))
	require.Equal(t, "enc:v1:sealed-marker", textColumn(t, `
		SELECT secret FROM mfa_credentials WHERE subject_type = 'users' AND subject_id = ?`, userID))
	require.Equal(t, int64(1), countRows(t, `
		SELECT count(*) FROM mfa_backup_codes
		WHERE subject_type = 'users' AND subject_id = ? AND code_hash = 'backup-hash-marker'`, userID))
}

func TestDropLegacyTotpSecretRefusesAValueThatWasNotCleared(t *testing.T) {
	fixtures.TestDB(t)

	migration := &migrations.M00000000000550DropLegacyTotpSecret{}
	require.NoError(t, migration.Down())

	userID := uuid.New()
	_, err := facades.Orm().Query().Exec(`
		INSERT INTO users (id, email, password_hash, status, totp_secret, created_at, updated_at)
		VALUES (?, 'leftover-totp@example.com', 'hash', 'active', 'leftover-marker', NOW(), NOW())`,
		userID)
	require.NoError(t, err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO totp_recovery_codes (id, user_id, code_hash, created_at, updated_at)
		VALUES (?, ?, 'leftover-code-marker', NOW(), NOW())`,
		uuid.New(), userID)
	require.NoError(t, err)

	err = migration.Up()
	require.Error(t, err)
	require.NotContains(t, err.Error(), "leftover-marker")
	require.NotContains(t, err.Error(), "leftover-code-marker")
	require.Equal(t, "leftover-marker", textColumn(t, `SELECT COALESCE(totp_secret, '') FROM users WHERE id = ?`, userID))
	require.Equal(t, int64(1), countRows(t, `SELECT count(*) FROM totp_recovery_codes WHERE user_id = ?`, userID))

	_, err = facades.Orm().Query().Exec(`UPDATE users SET totp_secret = NULL WHERE id = ?`, userID)
	require.NoError(t, err)
	err = migration.Up()
	require.Error(t, err)
	require.NotContains(t, err.Error(), "leftover-code-marker")
	require.True(t, legacyTotpSecretColumnPresent(t))
	require.True(t, legacyRecoveryTablePresent(t))

	_, err = facades.Orm().Query().Exec(`DELETE FROM totp_recovery_codes WHERE user_id = ?`, userID)
	require.NoError(t, err)
	require.NoError(t, migration.Up())
	require.False(t, legacyTotpSecretColumnPresent(t))
	require.False(t, legacyRecoveryTablePresent(t))
}

func legacyTotpSecretColumnPresent(t *testing.T) bool {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'users'
		  AND column_name = 'totp_secret'`) == 1
}

func legacyRecoveryTablePresent(t *testing.T) bool {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'totp_recovery_codes'`) == 1
}

func countRows(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, facades.Orm().Query().Raw(query, args...).Scan(&n))
	return n
}

func textColumn(t *testing.T, query string, args ...any) string {
	t.Helper()
	var value string
	require.NoError(t, facades.Orm().Query().Raw(query, args...).Scan(&value))
	return value
}
