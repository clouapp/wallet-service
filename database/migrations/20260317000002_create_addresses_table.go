package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260317000002CreateAddressesTable struct {
}

// Signature The unique signature for the migration.
func (r *M20260317000002CreateAddressesTable) Signature() string {
	return "20260317000002_create_addresses_table"
}

// Up Run the migrations.
func (r *M20260317000002CreateAddressesTable) Up() error {
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
		table.Timestamps()

		// Indexes
		table.Index("wallet_id")
		table.Index("external_user_id")
		table.Index("is_active")
		table.Index("chain", "address") // Composite index for address lookups
		table.Unique("address")         // Unique address across all chains

		// Foreign key
		table.Foreign("wallet_id").References("id").On("wallets")

		table.Comment("Derived addresses for deposit scanning")
	})
}

// Down Reverse the migrations.
func (r *M20260317000002CreateAddressesTable) Down() error {
	return facades.Schema().DropIfExists("addresses")
}
