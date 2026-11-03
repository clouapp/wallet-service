package migrations

import "github.com/goravel/framework/contracts/database/schema"

// M00000000000740CreateSwapCreditAllocations records exclusive payout allocations.
type M00000000000740CreateSwapCreditAllocations struct{}

func (*M00000000000740CreateSwapCreditAllocations) Signature() string {
	return "00000000000740_create_swap_credit_allocations"
}
func (*M00000000000740CreateSwapCreditAllocations) Up() error {
	if migrationSchema().HasTable("swap_credit_allocations") {
		return nil
	}
	return migrationSchema().Create("swap_credit_allocations", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("order_id")
		table.Uuid("wallet_id")
		table.String("direction", 8)
		table.String("chain", 50)
		for _, name := range []string{"asset_key", "address", "tx_hash", "amount_raw"} {
			table.Text(name)
		}
		table.TimestampsTz()
		table.Unique("order_id", "direction")
		table.Index("chain", "tx_hash", "asset_key", "address")
		table.Foreign("order_id").References("id").On("swap_orders")
		table.Foreign("wallet_id").References("id").On("wallets")
	})
}
func (*M00000000000740CreateSwapCreditAllocations) Down() error {
	return migrationSchema().DropIfExists("swap_credit_allocations")
}
