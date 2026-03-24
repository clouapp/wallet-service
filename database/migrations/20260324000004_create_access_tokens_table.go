package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000004CreateAccessTokensTable struct{}

func (r *M20260324000004CreateAccessTokensTable) Signature() string {
	return "20260324000004_create_access_tokens_table"
}

func (r *M20260324000004CreateAccessTokensTable) Up() error {
	return facades.Schema().Create("access_tokens", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("account_id")
		table.Uuid("created_by").Nullable()
		table.String("name", 255)
		table.Text("token_hash")
		table.Text("permissions").Nullable()
		table.Text("ip_cidr").Nullable()
		table.Json("spending_limit").Nullable()
		table.Timestamp("valid_until").Nullable()
		table.Timestamps()
		table.Foreign("account_id").References("id").On("accounts").CascadeOnDelete()
		table.Index("account_id")
	})
}

func (r *M20260324000004CreateAccessTokensTable) Down() error {
	return facades.Schema().DropIfExists("access_tokens")
}
