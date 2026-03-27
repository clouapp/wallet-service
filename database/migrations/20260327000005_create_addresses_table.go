package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000005CreateAddressesTable struct{}

func (r *M20260327000005CreateAddressesTable) Signature() string {
	return "20260327000005_create_addresses_table"
}

func (r *M20260327000005CreateAddressesTable) Up() error {
	return facades.Schema().Create("addresses", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("wallet_id").Comment("Foreign key to wallets table")
		table.String("chain", 50).Comment("Blockchain identifier")
		table.String("address", 255).Comment("Blockchain address")
		table.Integer("derivation_index").Comment("HD derivation index (m/44'/60'/0'/0/{index})")
		table.String("external_user_id", 255).Comment("Client's user identifier")
		table.Text("metadata").Nullable().Comment("Optional JSON metadata")
		table.Boolean("is_active").Default(true).Comment("Whether address is active for deposits")
		table.String("label", 255).Nullable()
		table.Uuid("created_by").Nullable()
		table.Timestamps()

		table.Index("wallet_id")
		table.Index("external_user_id")
		table.Index("is_active")
		table.Index("chain", "address")
		table.Unique("address")
		table.Foreign("wallet_id").References("id").On("wallets")

		table.Comment("Derived addresses for deposit scanning")
	})
}

func (r *M20260327000005CreateAddressesTable) Down() error {
	return facades.Schema().DropIfExists("addresses")
}
