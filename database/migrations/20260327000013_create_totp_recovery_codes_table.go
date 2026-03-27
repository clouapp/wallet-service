package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000013CreateTotpRecoveryCodesTable struct{}

func (r *M20260327000013CreateTotpRecoveryCodesTable) Signature() string {
	return "20260327000013_create_totp_recovery_codes_table"
}

func (r *M20260327000013CreateTotpRecoveryCodesTable) Up() error {
	return facades.Schema().Create("totp_recovery_codes", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("user_id")
		table.Text("code_hash")
		table.Timestamp("used_at").Nullable()
		table.Timestamps()
		table.Foreign("user_id").References("id").On("users").CascadeOnDelete()
		table.Index("user_id")
	})
}

func (r *M20260327000013CreateTotpRecoveryCodesTable) Down() error {
	return facades.Schema().DropIfExists("totp_recovery_codes")
}
