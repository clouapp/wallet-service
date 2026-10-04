package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestAccountInvitesWalletRolesIsNullableJsonb(t *testing.T) {
	mocks.TestDB(t)

	require.Equal(t, "jsonb", walletRolesType(t))
	require.Equal(t, "YES", walletRolesNullable(t))

	account := mocks.InsertAccount(t, "invite-wallet-roles")
	ownerID := uuid.New()
	inviteID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, full_name, status, created_at, updated_at)
		VALUES (?, 'wallet-roles-owner@example.com', 'hash', 'Owner', 'active', NOW(), NOW())`, ownerID)
	exec(t, `INSERT INTO account_invites (id, account_id, email, role, token_hash, invited_by, expires_at, created_at, updated_at)
		VALUES (?, ?, 'wallet-roles@example.com', 'user', 'hash', ?, NOW() + INTERVAL '72 hours', NOW(), NOW())`,
		inviteID, account.ID, ownerID)
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM account_invites WHERE id = ? AND wallet_roles IS NULL`, inviteID))

	exec(t, `UPDATE account_invites SET wallet_roles = CAST(? AS jsonb) WHERE id = ?`, `{"wallet":"user"}`, inviteID)
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM account_invites WHERE id = ? AND wallet_roles = CAST(? AS jsonb)`, inviteID, `{"wallet":"user"}`))

	migration := &migrations.M00000000000560AddAccountInvitesWalletRoles{}
	require.NoError(t, migration.Down())
	require.Equal(t, int64(0), walletRolesColumnCount(t))
	require.Equal(t, "wallet-roles@example.com", scalar[string](t, `SELECT email FROM account_invites WHERE id = ?`, inviteID))
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM pg_indexes WHERE indexname = 'account_invites_pending_email'`))

	require.NoError(t, migration.Up())
	require.Equal(t, "jsonb", walletRolesType(t))
	require.Equal(t, "YES", walletRolesNullable(t))
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM account_invites WHERE id = ? AND wallet_roles IS NULL`, inviteID))
	require.NoError(t, migration.Up())
	require.Equal(t, int64(1), walletRolesColumnCount(t))
}

func walletRolesColumnCount(t *testing.T) int64 {
	t.Helper()
	return scalar[int64](t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'account_invites'
		  AND column_name = 'wallet_roles'`)
}

func walletRolesType(t *testing.T) string {
	t.Helper()
	return scalar[string](t, `
		SELECT udt_name FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'account_invites'
		  AND column_name = 'wallet_roles'`)
}

func walletRolesNullable(t *testing.T) string {
	t.Helper()
	return scalar[string](t, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'account_invites'
		  AND column_name = 'wallet_roles'`)
}
