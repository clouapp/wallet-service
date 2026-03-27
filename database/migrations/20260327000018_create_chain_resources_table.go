package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000018CreateChainResourcesTable struct{}

func (r *M20260327000018CreateChainResourcesTable) Signature() string {
	return "20260327000018_create_chain_resources_table"
}

func (r *M20260327000018CreateChainResourcesTable) Up() error {
	return facades.Schema().Create("chain_resources", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("chain_id", 20)
		table.String("type", 20)
		table.String("name", 100)
		table.String("url", 500)
		table.Text("description").Nullable()
		table.Integer("display_order").Default(0)
		table.String("status", 20).Default("active")
		table.Timestamps()

		table.Foreign("chain_id").References("id").On("chains").CascadeOnDelete()
	})
}

func (r *M20260327000018CreateChainResourcesTable) Down() error {
	return facades.Schema().DropIfExists("chain_resources")
}
