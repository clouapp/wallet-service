package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260317000001CreateWalletsTable struct {
}

// Signature The unique signature for the migration.
func (r *M20260317000001CreateWalletsTable) Signature() string {
	return "20260317000001_create_wallets_table"
}

// Up Run the migrations.
func (r *M20260317000001CreateWalletsTable) Up() error {
	return facades.Schema().Create("wallets", func(table schema.Blueprint) {
		table.Uuid("id").Primary()
		table.String("chain", 50).Comment("Blockchain identifier (eth, polygon, sol, btc)")
		table.String("label", 255).Nullable().Comment("User-friendly wallet label")
		table.Text("master_pubkey").Comment("HD wallet master public key")
		table.String("key_vault_ref", 255).Comment("Reference to key vault for private key")
		table.String("derivation_path", 100).Comment("BIP44 derivation path")
		table.Integer("address_index").Default(0).Comment("Current address derivation index")
		table.Timestamps()

		// Indexes
		table.Index("chain")
		table.Comment("HD wallets for multi-chain custody")
	})
}

// Down Reverse the migrations.
func (r *M20260317000001CreateWalletsTable) Down() error {
	return facades.Schema().DropIfExists("wallets")
}
