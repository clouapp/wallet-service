package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000002CreateAccountsTable struct{}

func (r *M20260327000002CreateAccountsTable) Signature() string {
	return "20260327000002_create_accounts_table"
}

func (r *M20260327000002CreateAccountsTable) Up() error {
	return facades.Schema().Create("accounts", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("name", 255)
		table.String("status", 20).Default("active")
		table.Boolean("view_all_wallets").Default(false)
		table.String("environment", 4).Default("prod")
		table.Uuid("linked_account_id").Nullable()
		table.Timestamps()

		table.Foreign("linked_account_id").References("id").On("accounts").NullOnDelete()
	})
}

func (r *M20260327000002CreateAccountsTable) Down() error {
	return facades.Schema().DropIfExists("accounts")
}
