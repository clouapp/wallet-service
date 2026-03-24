package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000009AlterAddressesAddMetadata struct{}

func (r *M20260324000009AlterAddressesAddMetadata) Signature() string {
	return "20260324000009_alter_addresses_add_metadata"
}

func (r *M20260324000009AlterAddressesAddMetadata) Up() error {
	return facades.Schema().Table("addresses", func(table schema.Blueprint) {
		table.String("label", 255).Nullable()
		table.Uuid("created_by").Nullable()
	})
}

func (r *M20260324000009AlterAddressesAddMetadata) Down() error {
	return facades.Schema().Table("addresses", func(table schema.Blueprint) {
		table.DropColumn("label")
		table.DropColumn("created_by")
	})
}
