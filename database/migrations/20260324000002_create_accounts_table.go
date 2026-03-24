package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000002CreateAccountsTable struct{}

func (r *M20260324000002CreateAccountsTable) Signature() string {
	return "20260324000002_create_accounts_table"
}

func (r *M20260324000002CreateAccountsTable) Up() error {
	return facades.Schema().Create("accounts", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("name", 255)
		table.String("status", 20).Default("active")
		table.Boolean("view_all_wallets").Default(false)
		table.Timestamps()
	})
}

func (r *M20260324000002CreateAccountsTable) Down() error {
	return facades.Schema().DropIfExists("accounts")
}
