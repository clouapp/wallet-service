package migrations

import "github.com/goravel/framework/facades"

type M00000000000190CreateWalletUsersTable struct{}

func (r *M00000000000190CreateWalletUsersTable) Signature() string {
	return "00000000000190_create_wallet_users_table"
}

func (r *M00000000000190CreateWalletUsersTable) Up() error {
	stmts := []string{
		`CREATE TABLE wallet_users (
			id         UUID        NOT NULL DEFAULT gen_random_uuid(),
			wallet_id  UUID        NOT NULL,
			user_id    UUID        NOT NULL,
			roles      TEXT,
			status     VARCHAR(20) NOT NULL DEFAULT 'active',
			deleted_at TIMESTAMP WITHOUT TIME ZONE,
			created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT NOW(),
			CONSTRAINT wallet_users_pkey           PRIMARY KEY (id),
			CONSTRAINT wallet_users_wallet_id_fkey FOREIGN KEY (wallet_id) REFERENCES wallets(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_wallet_users_wallet_id ON wallet_users (wallet_id)`,
		`CREATE INDEX idx_wallet_users_user_id    ON wallet_users (user_id)`,
		`CREATE UNIQUE INDEX wallet_users_active_unique ON wallet_users (wallet_id, user_id) WHERE deleted_at IS NULL`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000190CreateWalletUsersTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallet_users`)
	return err
}
