package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"
)

// insertActiveUserRow creates an active user with raw SQL so the NOT NULL
// preferences column takes its '{}' default; UserRepository.Create writes
// NULL there and fails.
func insertActiveUserRow(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, 'h', 'active', NOW(), NOW())`,
		id, "user-"+id.String()+"@test.com",
	)
	require.NoError(t, err)
	return id
}
