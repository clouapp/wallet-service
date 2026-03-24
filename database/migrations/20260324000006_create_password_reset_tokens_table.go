package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000006CreatePasswordResetTokensTable struct{}

func (r *M20260324000006CreatePasswordResetTokensTable) Signature() string {
	return "20260324000006_create_password_reset_tokens_table"
}

func (r *M20260324000006CreatePasswordResetTokensTable) Up() error {
	return facades.Schema().Create("password_reset_tokens", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("user_id")
		table.Text("token_hash")
		table.Timestamp("expires_at")
		table.Timestamp("used_at").Nullable()
		table.Timestamps()
		table.Foreign("user_id").References("id").On("users").CascadeOnDelete()
	})
}

func (r *M20260324000006CreatePasswordResetTokensTable) Down() error {
	return facades.Schema().DropIfExists("password_reset_tokens")
}
