package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000001CreateChainsTable struct{}

func (r *M20260327000001CreateChainsTable) Signature() string {
	return "20260327000001_create_chains_table"
}

func (r *M20260327000001CreateChainsTable) Up() error {
	return facades.Schema().Create("chains", func(table schema.Blueprint) {
		table.String("id", 20)
		table.Primary("id")
		table.String("name", 100)
		table.String("adapter_type", 20)
		table.String("native_symbol", 20)
		table.Integer("native_decimals")
		table.BigInteger("network_id").Nullable()
		table.Text("rpc_url")
		table.Boolean("is_testnet").Default(false)
		table.String("mainnet_chain_id", 20).Nullable()
		table.Integer("required_confirmations")
		table.String("icon_url", 500).Nullable()
		table.Integer("display_order").Default(0)
		table.String("status", 20).Default("active")
		table.Timestamps()

		table.Foreign("mainnet_chain_id").References("id").On("chains").NullOnDelete()
	})
}

func (r *M20260327000001CreateChainsTable) Down() error {
	return facades.Schema().DropIfExists("chains")
}
