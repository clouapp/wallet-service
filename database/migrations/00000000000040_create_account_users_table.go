package migrations

import "github.com/goravel/framework/facades"

type M00000000000040CreateAccountUsersTable struct{}

func (r *M00000000000040CreateAccountUsersTable) Signature() string {
	return "00000000000040_create_account_users_table"
}

func (r *M00000000000040CreateAccountUsersTable) Up() error {
	stmts := []string{
		`CREATE TABLE account_users (
			id         UUID        NOT NULL DEFAULT gen_random_uuid(),
			account_id UUID        NOT NULL,
			user_id    UUID        NOT NULL,
			role       VARCHAR(20) NOT NULL,
			status     VARCHAR(20) NOT NULL DEFAULT 'active',
			added_by   UUID,
			deleted_at TIMESTAMP WITHOUT TIME ZONE,
			created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NOW(),
			CONSTRAINT account_users_pkey PRIMARY KEY (id),
			CONSTRAINT account_users_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
			CONSTRAINT account_users_user_id_fkey    FOREIGN KEY (user_id)    REFERENCES users(id)    ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_account_users_account_id ON account_users (account_id)`,
		`CREATE INDEX idx_account_users_user_id    ON account_users (user_id)`,
		`CREATE UNIQUE INDEX account_users_active_unique ON account_users (account_id, user_id) WHERE deleted_at IS NULL`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000040CreateAccountUsersTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS account_users`)
	return err
}
