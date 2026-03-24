package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260324000003CreateAccountUsersTable struct{}

func (r *M20260324000003CreateAccountUsersTable) Signature() string {
	return "20260324000003_create_account_users_table"
}

func (r *M20260324000003CreateAccountUsersTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE IF NOT EXISTS account_users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			role VARCHAR(20) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			added_by UUID,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	_, err = facades.Orm().Query().Exec(`CREATE INDEX IF NOT EXISTS idx_account_users_account_id ON account_users (account_id)`)
	if err != nil {
		return err
	}
	_, err = facades.Orm().Query().Exec(`CREATE INDEX IF NOT EXISTS idx_account_users_user_id ON account_users (user_id)`)
	if err != nil {
		return err
	}
	_, err = facades.Orm().Query().Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS account_users_active_unique ON account_users (account_id, user_id) WHERE deleted_at IS NULL`,
	)
	return err
}

func (r *M20260324000003CreateAccountUsersTable) Down() error {
	return facades.Schema().DropIfExists("account_users")
}
