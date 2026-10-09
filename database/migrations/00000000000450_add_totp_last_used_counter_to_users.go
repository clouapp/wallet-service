package migrations

type M00000000000450AddTotpLastUsedCounterToUsers struct{}

func (r *M00000000000450AddTotpLastUsedCounterToUsers) Signature() string {
	return "00000000000450_add_totp_last_used_counter_to_users"
}

func (r *M00000000000450AddTotpLastUsedCounterToUsers) Up() error {
	_, err := migrationQuery().Exec(
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_used_counter BIGINT NOT NULL DEFAULT 0`,
	)
	return err
}

func (r *M00000000000450AddTotpLastUsedCounterToUsers) Down() error {
	_, err := migrationQuery().Exec(`ALTER TABLE users DROP COLUMN IF EXISTS totp_last_used_counter`)
	return err
}
