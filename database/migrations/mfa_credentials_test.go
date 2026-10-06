package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestCopyLegacyTotpSealsTheSecretAndMovesRecoveryCodes(t *testing.T) {
	fixtures.TestDB(t)
	require.NoError(t, (&migrations.M00000000000550DropLegacyTotpSecret{}).Down())

	userID := uuid.New()
	_, err := facades.Orm().Query().Exec(`
		INSERT INTO users (id, email, password_hash, status, totp_secret, totp_enabled, totp_last_used_counter, created_at, updated_at)
		VALUES (?, 'legacy-totp@example.com', 'hash', 'active', 'legacy-crypt-blob', TRUE, 9, NOW(), NOW())`,
		userID)
	require.NoError(t, err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO totp_recovery_codes (id, user_id, code_hash, created_at, updated_at)
		VALUES (?, ?, 'recovery-hash-marker', NOW(), NOW())`,
		uuid.New(), userID)
	require.NoError(t, err)

	require.NoError(t, (&migrations.M00000000000510CreateMfaBackupCodesTable{}).Up())

	var stored string
	require.NoError(t, facades.Orm().Query().Raw(`
		SELECT secret FROM mfa_credentials WHERE subject_type = 'users' AND subject_id = ?`, userID).Scan(&stored))
	if stored != "enc:v1:legacy-crypt-blob" {
		t.Fatal("copied totp secret was not sealed")
	}
	var column string
	require.NoError(t, facades.Orm().Query().Raw(`
		SELECT COALESCE(totp_secret, '') FROM users WHERE id = ?`, userID).Scan(&column))
	require.Empty(t, column)
	var counter int64
	require.NoError(t, facades.Orm().Query().Raw(`
		SELECT last_used_counter FROM mfa_credentials WHERE subject_type = 'users' AND subject_id = ?`, userID).Scan(&counter))
	require.Equal(t, int64(9), counter)
	var confirmed int64
	require.NoError(t, facades.Orm().Query().Raw(`
		SELECT count(*) FROM mfa_credentials
		WHERE subject_type = 'users' AND subject_id = ? AND confirmed_at IS NOT NULL`, userID).Scan(&confirmed))
	require.Equal(t, int64(1), confirmed)
	var backups int64
	require.NoError(t, facades.Orm().Query().Raw(`
		SELECT count(*) FROM mfa_backup_codes
		WHERE subject_type = 'users' AND subject_id = ? AND code_hash = 'recovery-hash-marker'`, userID).Scan(&backups))
	require.Equal(t, int64(1), backups)
	var left int64
	require.NoError(t, facades.Orm().Query().Raw(`
		SELECT count(*) FROM totp_recovery_codes WHERE user_id = ?`, userID).Scan(&left))
	require.Zero(t, left)

	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, last_used_counter, created_at, updated_at
		) VALUES (?, 'platform_admins', ?, 'enc:v1:platform-marker', 0, NOW(), NOW())`,
		uuid.New(), uuid.New())
	require.NoError(t, err)
	var subjects int64
	require.NoError(t, facades.Orm().Query().Raw(`
		SELECT count(DISTINCT subject_type) FROM mfa_credentials`).Scan(&subjects))
	require.Equal(t, int64(2), subjects)

	require.NoError(t, (&migrations.M00000000000510CreateMfaBackupCodesTable{}).Up())
}

func TestCopyLegacyTotpLeavesTheColumnWhenTheCopyDoesNotMatch(t *testing.T) {
	fixtures.TestDB(t)
	require.NoError(t, (&migrations.M00000000000550DropLegacyTotpSecret{}).Down())

	userID := uuid.New()
	_, err := facades.Orm().Query().Exec(`
		INSERT INTO users (id, email, password_hash, status, totp_secret, totp_enabled, created_at, updated_at)
		VALUES (?, 'mismatch-totp@example.com', 'hash', 'active', 'different-blob', TRUE, NOW(), NOW())`,
		userID)
	require.NoError(t, err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, last_used_counter, created_at, updated_at
		) VALUES (?, 'users', ?, 'enc:v1:already-different', 3, NOW(), NOW())`,
		uuid.New(), userID)
	require.NoError(t, err)

	err = (&migrations.M00000000000510CreateMfaBackupCodesTable{}).Up()
	require.Error(t, err)

	var column string
	require.NoError(t, facades.Orm().Query().Raw(`
		SELECT COALESCE(totp_secret, '') FROM users WHERE id = ?`, userID).Scan(&column))
	if column != "different-blob" {
		t.Fatal("totp secret was cleared although the copy did not match")
	}
}
