package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestAccess_Token_PermissionsBecomeNullableJsonb(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000570AccessTokensPermissionsJsonb{}

	require.Equal(t, "jsonb", permissionsColumnType(t))
	require.Equal(t, "YES", permissionsNullable(t))
	require.Equal(t, int64(1), constraintCount(t, "access_tokens_permissions_array"))
	require.NoError(t, migration.Up())
	require.Equal(t, int64(1), permissionsColumnCount(t))

	require.NoError(t, migration.Down())
	require.Equal(t, "text", permissionsColumnType(t))
	require.Zero(t, constraintCount(t, "access_tokens_permissions_array"))
	require.NoError(t, migration.Down())

	account := fixtures.InsertAccount(t, "token-permissions")
	blankID := insertLegacyPermission(t, account.ID, "")
	spaceID := insertLegacyPermission(t, account.ID, "   ")
	arrayID := insertLegacyPermission(t, account.ID, `  ["webhooks.read"]  `)
	legacyID := insertLegacyPermission(t, account.ID, "read")
	objectID := insertLegacyPermission(t, account.ID, `{"daily_usd":"1"}`)
	nullID := insertLegacyPermission(t, account.ID, "")
	exec(t, `UPDATE access_tokens SET permissions = NULL WHERE id = ?`, nullID)

	require.NoError(t, migration.Up())
	require.Equal(t, "jsonb", permissionsColumnType(t))
	require.Equal(t, int64(1), constraintCount(t, "access_tokens_permissions_array"))
	require.Equal(t, int64(1), permissionsColumnCount(t))
	requireNullPermissions(t, blankID)
	requireNullPermissions(t, spaceID)
	requireNullPermissions(t, nullID)
	require.Equal(t, `["webhooks.read"]`, permissionText(t, arrayID))
	require.Equal(t, "[]", permissionText(t, legacyID))
	require.Equal(t, "[]", permissionText(t, objectID))

	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, permissions, spending_limit, created_at, updated_at)
		 VALUES (?, ?, 'object', 'not-a-secret', '{"a":1}'::jsonb, '{}', NOW(), NOW())`,
		uuid.New(), account.ID,
	)
	require.Error(t, err)

	kept := uuid.New()
	exec(t, `INSERT INTO access_tokens (id, account_id, name, token_hash, permissions, spending_limit, created_at, updated_at)
		VALUES (?, ?, 'array', 'not-a-secret', '["wallets.read"]'::jsonb, '{}', NOW(), NOW())`,
		kept, account.ID)
	exec(t, `INSERT INTO access_tokens (id, account_id, name, token_hash, spending_limit, created_at, updated_at)
		VALUES (?, ?, 'omitted', 'not-a-secret', '{}', NOW(), NOW())`,
		uuid.New(), account.ID)
	require.Equal(t, `["wallets.read"]`, permissionText(t, kept))

	require.NoError(t, migration.Up())
	require.Equal(t, `["wallets.read"]`, permissionText(t, kept))
	require.Equal(t, int64(1), permissionsColumnCount(t))
}

func insertLegacyPermission(t *testing.T, accountID uuid.UUID, permissions string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, `INSERT INTO access_tokens (id, account_id, name, token_hash, permissions, spending_limit, created_at, updated_at)
		VALUES (?, ?, ?, 'not-a-secret', ?, '{}', NOW(), NOW())`,
		id, accountID, "legacy-"+id.String()[:8], permissions)
	return id
}

func requireNullPermissions(t *testing.T, id uuid.UUID) {
	t.Helper()
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM access_tokens WHERE id = ? AND permissions IS NULL`, id))
}

func permissionText(t *testing.T, id uuid.UUID) string {
	t.Helper()
	return scalar[string](t, `SELECT permissions::text FROM access_tokens WHERE id = ?`, id)
}

func permissionsColumnCount(t *testing.T) int64 {
	t.Helper()
	return scalar[int64](t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'access_tokens'
		  AND column_name = 'permissions'`)
}

func permissionsColumnType(t *testing.T) string {
	t.Helper()
	return scalar[string](t, `
		SELECT udt_name FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'access_tokens'
		  AND column_name = 'permissions'`)
}

func permissionsNullable(t *testing.T) string {
	t.Helper()
	return scalar[string](t, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'access_tokens'
		  AND column_name = 'permissions'`)
}
