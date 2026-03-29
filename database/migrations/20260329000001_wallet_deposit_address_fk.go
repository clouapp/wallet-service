package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260329000001WalletDepositAddressFk struct{}

func (r *M20260329000001WalletDepositAddressFk) Signature() string {
	return "20260329000001_wallet_deposit_address_fk"
}

func (r *M20260329000001WalletDepositAddressFk) Up() error {
	return facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.Uuid("deposit_address_id").Nullable()
		table.DropColumn("deposit_address")
	})
}

func (r *M20260329000001WalletDepositAddressFk) Down() error {
	return facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.Text("deposit_address").Nullable()
		table.DropColumn("deposit_address_id")
	})
}
