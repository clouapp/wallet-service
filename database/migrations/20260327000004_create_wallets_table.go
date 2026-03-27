package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000004CreateWalletsTable struct{}

func (r *M20260327000004CreateWalletsTable) Signature() string {
	return "20260327000004_create_wallets_table"
}

func (r *M20260327000004CreateWalletsTable) Up() error {
	return facades.Schema().Create("wallets", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("chain", 50).Comment("Blockchain identifier (eth, polygon, sol, btc)")
		table.String("label", 255).Nullable().Comment("User-friendly wallet label")
		table.Text("mpc_customer_share").Comment("Hex-encoded AES-256-GCM encrypted share_A (ciphertext || 16-byte tag)")
		table.Text("mpc_share_iv").Comment("Hex-encoded AES-256-GCM nonce, exactly 12 bytes")
		table.Text("mpc_share_salt").Comment("Hex-encoded Argon2id salt, exactly 16 bytes")
		table.Text("mpc_secret_arn").Comment("AWS Secrets Manager ARN for share_B")
		table.Text("mpc_public_key").Comment("Hex-encoded compressed public key (33 bytes secp256k1 / 32 bytes ed25519)")
		table.String("mpc_curve", 20).Comment("secp256k1 or ed25519")
		table.Text("deposit_address").Comment("Blockchain deposit address derived from combined public key")
		table.Uuid("account_id").Nullable()
		table.String("status", 20).Default("pending")
		table.Integer("fee_rate_min").Nullable()
		table.Integer("fee_rate_max").Nullable()
		table.Decimal("fee_multiplier").Places(4).Total(8).Nullable()
		table.Integer("required_approvals").Default(1)
		table.Timestamp("frozen_until").Nullable()
		table.String("activation_code", 6).Nullable()
		table.Timestamps()

		table.Index("chain")
		table.Comment("MPC co-signing wallets")
	})
}

func (r *M20260327000004CreateWalletsTable) Down() error {
	return facades.Schema().DropIfExists("wallets")
}
