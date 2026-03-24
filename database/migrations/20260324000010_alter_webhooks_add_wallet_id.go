package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000010AlterWebhooksAddWalletId struct{}

func (r *M20260324000010AlterWebhooksAddWalletId) Signature() string {
	return "20260324000010_alter_webhooks_add_wallet_id"
}

func (r *M20260324000010AlterWebhooksAddWalletId) Up() error {
	return facades.Schema().Table("webhook_configs", func(table schema.Blueprint) {
		table.Uuid("wallet_id").Nullable()
		table.String("type", 50).Nullable()
		table.Index("wallet_id")
	})
}

func (r *M20260324000010AlterWebhooksAddWalletId) Down() error {
	return facades.Schema().Table("webhook_configs", func(table schema.Blueprint) {
		table.DropColumn("wallet_id")
		table.DropColumn("type")
	})
}
