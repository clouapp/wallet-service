package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000015AlterWalletsAddKeycardFields struct{}

func (r *M20260324000015AlterWalletsAddKeycardFields) Signature() string {
	return "20260324000015_alter_wallets_add_keycard_fields"
}

func (r *M20260324000015AlterWalletsAddKeycardFields) Up() error {
	if err := facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.String("activation_code", 6).Nullable()
	}); err != nil {
		return err
	}
	// Goravel Blueprint doesn't expose ChangeDefault — use raw SQL
	_, err := facades.Orm().Query().Exec(
		"ALTER TABLE wallets ALTER COLUMN status SET DEFAULT 'pending'",
	)
	return err
}

func (r *M20260324000015AlterWalletsAddKeycardFields) Down() error {
	_, err := facades.Orm().Query().Exec(
		"ALTER TABLE wallets ALTER COLUMN status SET DEFAULT 'active'",
	)
	if err != nil {
		return err
	}
	return facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.DropColumn("activation_code")
	})
}
