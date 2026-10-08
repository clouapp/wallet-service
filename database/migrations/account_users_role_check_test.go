package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/stretchr/testify/assert"
)

func TestAccount_Users_RoleCheckRewritesViewerAndRejectsIt(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000470AccountUsersRoleCheck{}
	assert.Equal(t, int64(1), constraintCount(t, "account_users_role_check"))

	require.NoError(t, migration.Down())
	assert.Zero(t, constraintCount(t, "account_users_role_check"))

	account := fixtures.InsertAccount(t, "role-check")
	userID := uuid.New()
	membershipID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		VALUES (?, ?, 'hash', 'active', NOW(), NOW())`, userID, userID.String()+"@example.com")
	exec(t, `INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		VALUES (?, ?, ?, 'viewer', 'active', NOW(), NOW())`, membershipID, account.ID, userID)

	require.NoError(t, migration.Up())
	assert.Equal(t, "auditor", scalar[string](t, `SELECT role FROM account_users WHERE id = ?`, membershipID))
	assert.Equal(t, int64(1), constraintCount(t, "account_users_role_check"))

	otherID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		VALUES (?, ?, 'hash', 'active', NOW(), NOW())`, otherID, otherID.String()+"@example.com")
	_, err := facades.Orm().Query().Exec(`INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		VALUES (?, ?, ?, 'viewer', 'active', NOW(), NOW())`, uuid.New(), account.ID, otherID)
	assert.Error(t, err)
}
