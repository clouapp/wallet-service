package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000006CreateTransactionsTable struct{}

func (r *M20260327000006CreateTransactionsTable) Signature() string {
	return "20260327000006_create_transactions_table"
}

func (r *M20260327000006CreateTransactionsTable) Up() error {
	return facades.Schema().Create("transactions", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("address_id").Nullable().Comment("Deposit address (nullable for withdrawals)")
		table.Uuid("wallet_id").Comment("Foreign key to wallets table")
		table.String("external_user_id", 255).Comment("Client's user identifier")
		table.String("chain", 50).Comment("Blockchain identifier")
		table.String("tx_type", 20).Comment("deposit or withdrawal")
		table.String("type", 20).Nullable().Comment("Transaction type for filtering")
		table.String("tx_hash", 255).Nullable().Comment("Blockchain transaction hash")
		table.String("from_address", 255).Nullable().Comment("Source address")
		table.String("to_address", 255).Comment("Destination address")
		table.String("amount", 100).Comment("Amount (stored as string for precision)")
		table.String("asset", 50).Comment("Asset symbol (ETH, USDC, SOL, BTC, etc.)")
		table.String("token_contract", 255).Nullable().Comment("ERC20/SPL token contract address")
		table.Integer("confirmations").Default(0).Comment("Current confirmation count")
		table.Integer("required_confs").Default(12).Comment("Required confirmations for finality")
		table.String("status", 20).Comment("pending, confirmed, failed")
		table.String("fee", 100).Nullable().Comment("Transaction fee (string for precision)")
		table.BigInteger("block_number").Nullable().Comment("Block number")
		table.String("block_hash", 255).Nullable().Comment("Block hash")
		table.Text("error_message").Nullable().Comment("Error details if failed")
		table.String("idempotency_key", 255).Nullable().Comment("Prevents duplicate withdrawals")
		table.Timestamp("confirmed_at").Nullable().Comment("Timestamp when tx reached required confirmations")
		table.Timestamps()

		table.Index("address_id")
		table.Index("wallet_id")
		table.Index("external_user_id")
		table.Index("tx_type")
		table.Index("status")
		table.Index("chain", "tx_hash")
		table.Index("chain", "status")
		table.Index("created_at")
		table.Unique("idempotency_key")
		table.Foreign("address_id").References("id").On("addresses")
		table.Foreign("wallet_id").References("id").On("wallets")

		table.Comment("Transaction history for deposits and withdrawals")
	})
}

func (r *M20260327000006CreateTransactionsTable) Down() error {
	return facades.Schema().DropIfExists("transactions")
}
