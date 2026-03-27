package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260327000015CreateWalletUsersTable struct{}

func (r *M20260327000015CreateWalletUsersTable) Signature() string {
	return "20260327000015_create_wallet_users_table"
}

func (r *M20260327000015CreateWalletUsersTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE IF NOT EXISTS wallet_users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
			user_id UUID NOT NULL,
			roles TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	_, err = facades.Orm().Query().Exec(`CREATE INDEX IF NOT EXISTS idx_wallet_users_wallet_id ON wallet_users (wallet_id)`)
	if err != nil {
		return err
	}
	_, err = facades.Orm().Query().Exec(`CREATE INDEX IF NOT EXISTS idx_wallet_users_user_id ON wallet_users (user_id)`)
	if err != nil {
		return err
	}
	_, err = facades.Orm().Query().Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS wallet_users_active_unique ON wallet_users (wallet_id, user_id) WHERE deleted_at IS NULL`,
	)
	return err
}

func (r *M20260327000015CreateWalletUsersTable) Down() error {
	return facades.Schema().DropIfExists("wallet_users")
}
