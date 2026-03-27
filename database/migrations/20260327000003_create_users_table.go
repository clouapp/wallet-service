package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000003CreateUsersTable struct{}

func (r *M20260327000003CreateUsersTable) Signature() string {
	return "20260327000003_create_users_table"
}

func (r *M20260327000003CreateUsersTable) Up() error {
	return facades.Schema().Create("users", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("email", 255)
		table.Text("password_hash")
		table.String("full_name", 255).Nullable()
		table.Text("totp_secret").Nullable()
		table.Boolean("totp_enabled").Default(false)
		table.String("status", 20).Default("active")
		table.Uuid("default_account_id").Nullable()
		table.Timestamps()

		table.Unique("email")
		table.Index("email")
		table.Foreign("default_account_id").References("id").On("accounts").NullOnDelete()
	})
}

func (r *M20260327000003CreateUsersTable) Down() error {
	return facades.Schema().DropIfExists("users")
}
