package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000011CreateRefreshTokensTable struct{}

func (r *M20260327000011CreateRefreshTokensTable) Signature() string {
	return "20260327000011_create_refresh_tokens_table"
}

func (r *M20260327000011CreateRefreshTokensTable) Up() error {
	return facades.Schema().Create("refresh_tokens", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("user_id")
		table.Text("token_hash")
		table.Timestamp("expires_at")
		table.Timestamp("revoked_at").Nullable()
		table.Timestamps()
		table.Foreign("user_id").References("id").On("users").CascadeOnDelete()
		table.Index("user_id")
	})
}

func (r *M20260327000011CreateRefreshTokensTable) Down() error {
	return facades.Schema().DropIfExists("refresh_tokens")
}
