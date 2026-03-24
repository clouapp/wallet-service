package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000012CreateWithdrawalsTable struct{}

func (r *M20260324000012CreateWithdrawalsTable) Signature() string {
	return "20260324000012_create_withdrawals_table"
}

func (r *M20260324000012CreateWithdrawalsTable) Up() error {
	return facades.Schema().Create("withdrawals", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("wallet_id")
		table.Uuid("transaction_id").Nullable()
		table.Uuid("account_id").Nullable()
		table.String("status", 20).Default("pending")
		table.Decimal("amount").Places(18).Total(36)
		table.Text("destination_address")
		table.Decimal("fee_estimate").Places(18).Total(36).Nullable()
		table.Text("note").Nullable()
		table.Uuid("created_by").Nullable()
		table.Timestamps()
		table.Foreign("wallet_id").References("id").On("wallets").CascadeOnDelete()
		table.Index("wallet_id")
		table.Index("account_id")
	})
}

func (r *M20260324000012CreateWithdrawalsTable) Down() error {
	return facades.Schema().DropIfExists("withdrawals")
}
