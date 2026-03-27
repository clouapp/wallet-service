package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000017CreateTokensTable struct{}

func (r *M20260327000017CreateTokensTable) Signature() string {
	return "20260327000017_create_tokens_table"
}

func (r *M20260327000017CreateTokensTable) Up() error {
	return facades.Schema().Create("tokens", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("chain_id", 20)
		table.String("symbol", 20)
		table.String("name", 100)
		table.String("contract_address", 255)
		table.Integer("decimals")
		table.String("icon_url", 500).Nullable()
		table.String("status", 20).Default("active")
		table.Timestamps()

		table.Foreign("chain_id").References("id").On("chains").CascadeOnDelete()
	})
}

func (r *M20260327000017CreateTokensTable) Down() error {
	return facades.Schema().DropIfExists("tokens")
}
