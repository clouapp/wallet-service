package migrations

import "github.com/goravel/framework/contracts/database/schema"

// M00000000000720CreateSwaps persists quotes and idempotent, account-scoped swap orders.
type M00000000000720CreateSwaps struct{}

// Signature identifies the migration.
func (*M00000000000720CreateSwaps) Signature() string { return "00000000000720_create_swaps" }

// Up creates quote and order tables; signing credentials are never stored.
func (*M00000000000720CreateSwaps) Up() error {
	if !migrationSchema().HasTable("swap_quotes") {
		if err := migrationSchema().Create("swap_quotes", func(table schema.Blueprint) {
			table.Uuid("id")
			table.Primary("id")
			table.Uuid("account_id")
			table.Uuid("source_wallet_id")
			table.Uuid("destination_wallet_id")
			table.String("provider", 16)
			table.Text("route_id")
			table.Text("request_payload")
			table.Text("expected_raw")
			table.Text("minimum_raw")
			table.Text("fees_payload")
			table.Text("attempts_payload")
			table.Text("route_payload")
			table.Integer("estimated_seconds").Default(0)
			table.TimestampTz("expires_at")
			table.TimestampsTz()
			table.Index("account_id", "source_wallet_id")
			table.Foreign("account_id").References("id").On("accounts")
			table.Foreign("source_wallet_id").References("id").On("wallets")
			table.Foreign("destination_wallet_id").References("id").On("wallets")
		}); err != nil {
			return err
		}
	}
	if !migrationSchema().HasTable("swap_orders") {
		if err := migrationSchema().Create("swap_orders", func(table schema.Blueprint) {
			table.Uuid("id")
			table.Primary("id")
			table.Uuid("account_id")
			table.Uuid("quote_id")
			table.Unique("quote_id")
			table.Uuid("idempotency_key")
			table.Uuid("source_wallet_id")
			table.Uuid("destination_wallet_id")
			table.String("status", 24)
			table.String("provider", 16)
			table.Text("funding_payload").Default("{}")
			for _, column := range []string{"source_tx_hash", "destination_tx_hash", "refund_tx_hash"} {
				table.Text(column).Default("")
			}
			table.Text("received_raw").Default("0")
			table.Text("refund_raw").Default("0")
			table.String("failure_code", 64).Default("")
			table.TimestampTz("last_checked_at").Nullable()
			table.Uuid("authorized_user_id").Nullable()
			table.Uuid("authorized_token_id").Nullable()
			table.TimestampTz("authorized_until")
			table.TimestampsTz()
			table.Unique("account_id", "idempotency_key")
			table.Index("account_id", "source_wallet_id")
			table.Index("status", "last_checked_at")
			table.Foreign("account_id").References("id").On("accounts")
			table.Foreign("quote_id").References("id").On("swap_quotes")
			table.Foreign("source_wallet_id").References("id").On("wallets")
			table.Foreign("destination_wallet_id").References("id").On("wallets")
		}); err != nil {
			return err
		}
	}
	if migrationSchema().HasTable("swap_execution_steps") {
		return nil
	}
	return migrationSchema().Create("swap_execution_steps", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("order_id")
		table.Uuid("transaction_id")
		table.Integer("position")
		table.String("purpose", 24)
		table.Text("tx_hash")
		table.TimestampsTz()
		table.Unique("order_id", "position")
		table.Foreign("order_id").References("id").On("swap_orders")
		table.Foreign("transaction_id").References("id").On("transactions")
	})
}

// Down removes orders before their referenced quotes.
func (*M00000000000720CreateSwaps) Down() error {
	if err := migrationSchema().DropIfExists("swap_execution_steps"); err != nil {
		return err
	}
	if err := migrationSchema().DropIfExists("swap_orders"); err != nil {
		return err
	}
	return migrationSchema().DropIfExists("swap_quotes")
}
