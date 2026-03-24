package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000001CreateUsersTable struct{}

func (r *M20260324000001CreateUsersTable) Signature() string {
	return "20260324000001_create_users_table"
}

func (r *M20260324000001CreateUsersTable) Up() error {
	return facades.Schema().Create("users", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("email", 255)
		table.Text("password_hash")
		table.String("full_name", 255).Nullable()
		table.Text("totp_secret").Nullable()
		table.Boolean("totp_enabled").Default(false)
		table.String("status", 20).Default("active")
		table.Timestamps()
		table.Unique("email")
		table.Index("email")
	})
}

func (r *M20260324000001CreateUsersTable) Down() error {
	return facades.Schema().DropIfExists("users")
}
